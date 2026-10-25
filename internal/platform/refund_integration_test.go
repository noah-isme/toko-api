package platform_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/platform"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// These tests drive the refund handlers against a real Postgres with all
// migrations applied. They are opt-in: set TEST_DATABASE_URL to run them.

const orderTotal = int64(100000)

// ---------------------------------------------------------------------------
// Fake gateway that models gateway idempotency on the refund key.

type fakeOutcome struct {
	res payment.RefundResult
	err error
}

type fakeGateway struct {
	mu          sync.Mutex
	requests    []payment.RefundRequest
	calls       int // Refund invocations
	realCalls   int // refunds that "moved money" (new key, success)
	seen        map[string]fakeOutcome
	result      payment.RefundResult
	err         error
	beforeReply func(ctx context.Context, req payment.RefundRequest) error
	ctxErrs     []error // ctx.Err() observed after beforeReply

	lookups       int
	lookupResults map[string]payment.RefundResult
	lookupErr     error
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{seen: map[string]fakeOutcome{}, lookupResults: map[string]payment.RefundResult{}}
}

func (f *fakeGateway) CreateIntent(context.Context, payment.IntentRequest) (payment.IntentResponse, error) {
	return payment.IntentResponse{}, errors.New("not implemented")
}

func (f *fakeGateway) VerifyWebhook(*http.Request, []byte) (payment.WebhookVerifyResult, error) {
	return payment.WebhookVerifyResult{}, errors.New("not implemented")
}

func (f *fakeGateway) Refund(ctx context.Context, req payment.RefundRequest) (payment.RefundResult, error) {
	f.mu.Lock()
	f.calls++
	f.requests = append(f.requests, req)
	if prev, ok := f.seen[req.RefundKey]; ok {
		if prev.err == nil {
			f.mu.Unlock()
			return prev.res, nil // idempotent replay, no new money movement
		}
		if payment.IsDefiniteRefundFailure(prev.err) {
			f.mu.Unlock()
			return payment.RefundResult{}, &payment.RefundError{Definite: true, Code: "DUPLICATE_KEY", Message: "refund key already used"}
		}
	}
	hook := f.beforeReply
	f.mu.Unlock()
	var hookErr error
	if hook != nil {
		hookErr = hook(ctx, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	res, err := f.result, f.err
	if hookErr != nil {
		err = hookErr
	}
	if err == nil && res.ProviderRef == "" {
		res = payment.RefundResult{ProviderRef: "rfd-" + req.RefundKey, Status: payment.RefundStatusSucceeded}
	}
	if err != nil {
		res = payment.RefundResult{}
	} else {
		f.realCalls++
	}
	f.seen[req.RefundKey] = fakeOutcome{res: res, err: err}
	return res, err
}

func (f *fakeGateway) set(res payment.RefundResult, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result, f.err = res, err
}

func (f *fakeGateway) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.requests))
	for _, r := range f.requests {
		out = append(out, r.RefundKey)
	}
	return out
}

func (f *fakeGateway) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeLookupGateway additionally implements payment.RefundLookupProvider.
type fakeLookupGateway struct{ *fakeGateway }

func (f fakeLookupGateway) LookupRefund(_ context.Context, req payment.RefundRequest) (payment.RefundResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups++
	if f.lookupErr != nil {
		return payment.RefundResult{}, false, f.lookupErr
	}
	res, ok := f.lookupResults[req.RefundKey]
	return res, ok, nil
}

// noRefundProvider is a checkout provider without refund support.
type noRefundProvider struct{}

func (noRefundProvider) CreateIntent(context.Context, payment.IntentRequest) (payment.IntentResponse, error) {
	return payment.IntentResponse{}, nil
}

func (noRefundProvider) VerifyWebhook(*http.Request, []byte) (payment.WebhookVerifyResult, error) {
	return payment.WebhookVerifyResult{}, nil
}

// ---------------------------------------------------------------------------
// Harness

type rtest struct {
	t         *testing.T
	pool      *pgxpool.Pool
	router    http.Handler
	handler   *platform.Handler
	gw        *fakeGateway
	tenA      string
	tenB      string
	userA     string
	userB     string
	midtransS *httptest.Server
	xenditS   *httptest.Server
}

type seeded struct {
	order, payment, ret string
}

func randHex(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func newRTest(t *testing.T) *rtest {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	e := &rtest{t: t, pool: pool, gw: newFakeGateway()}
	e.tenA = e.insertTenant()
	e.tenB = e.insertTenant()
	e.userA = e.insertUser()
	e.userB = e.insertUser()
	t.Cleanup(func() {
		cctx := context.Background()
		tenants, users := []string{e.tenA, e.tenB}, []string{e.userA, e.userB}
		for _, c := range []struct {
			stmt string
			ids  []string
		}{
			{`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`, tenants},
			{`DELETE FROM payments WHERE tenant_id = ANY($1::uuid[])`, tenants},
			{`DELETE FROM users WHERE id = ANY($1::uuid[])`, users},
			{`DELETE FROM tenants WHERE id = ANY($1::uuid[])`, tenants},
		} {
			stmt := c.stmt
			if _, err := pool.Exec(cctx, stmt, c.ids); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})

	e.handler = &platform.Handler{Pool: pool, Providers: map[string]payment.Provider{
		"fake":       e.gw,
		"fakelookup": fakeLookupGateway{e.gw},
		"norefund":   noRefundProvider{},
	}}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if id := req.Header.Get("X-Tenant-ID"); id != "" {
				ctx = tenant.WithTenant(ctx, id)
			}
			if id := req.Header.Get("X-User-ID"); id != "" {
				ctx = common.WithUserID(ctx, id)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/admin/returns", e.handler.AdminReturnList)
	r.Patch("/admin/returns/{returnId}", e.handler.AdminReturnStatus)
	r.Post("/admin/returns/{returnId}/refund", e.handler.AdminRefund)
	r.Get("/returns/{returnId}", e.handler.ReturnGet)
	e.router = r
	return e
}

func (e *rtest) insertTenant() string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `INSERT INTO tenants(slug,name) VALUES($1,$2) RETURNING id::text`, randHex("rf-tenant"), "Refund IT").Scan(&id))
	return id
}

func (e *rtest) insertUser() string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `INSERT INTO users(name,email,password_hash) VALUES('Refund IT',$1,'x') RETURNING id::text`, randHex("rf-user")+"@example.test").Scan(&id))
	return id
}

func (e *rtest) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

// seed creates a DELIVERED order with a PAID payment of provider and an APPROVED return.
func (e *rtest) seed(tenantID, userID, provider string) seeded {
	e.t.Helper()
	ctx := context.Background()
	var s seeded
	require.NoError(e.t, e.pool.QueryRow(ctx, `INSERT INTO orders(user_id,status,pricing_subtotal,pricing_total,tenant_id) VALUES($1,'DELIVERED',$2,$2,$3) RETURNING id::text`, userID, orderTotal, tenantID).Scan(&s.order))
	if provider != "" {
		require.NoError(e.t, e.pool.QueryRow(ctx, `INSERT INTO payments(order_id,provider,status,intent_token,amount,tenant_id) VALUES($1,$2,'PAID','inv-1',$3,$4) RETURNING id::text`, s.order, provider, orderTotal, tenantID).Scan(&s.payment))
	}
	s.ret = e.addReturn(tenantID, userID, s.order, "APPROVED")
	return s
}

func (e *rtest) addReturn(tenantID, userID, orderID, status string) string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `INSERT INTO returns(tenant_id,order_id,user_id,status,reason) VALUES($1,$2,$3,$4,'damaged') RETURNING id::text`, tenantID, orderID, userID, status).Scan(&id))
	return id
}

// seedRefundRow inserts a refunds row for s directly.
func (e *rtest) seedRefundRow(tenantID string, s seeded, status, providerRef string, providerStatus any, provider string) string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `INSERT INTO refunds(tenant_id,return_id,order_id,payment_id,provider,provider_ref,provider_status,amount,reason,status,attempt)
	    VALUES($1,$2,$3,$4,$5,$6,$7,$8,'customer return',$9,0) RETURNING id::text`,
		tenantID, s.ret, s.order, s.payment, provider, providerRef, providerStatus, orderTotal, status).Scan(&id))
	return id
}

type resp struct {
	Status int
	Body   map[string]any
	Raw    string
}

func (r resp) data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)
	return d
}

func (r resp) errCode() string {
	m, _ := r.Body["error"].(map[string]any)
	s, _ := m["code"].(string)
	return s
}

func (r resp) errMessage() string {
	m, _ := r.Body["error"].(map[string]any)
	s, _ := m["message"].(string)
	return s
}

func (r resp) details() map[string]any {
	m, _ := r.Body["error"].(map[string]any)
	d, _ := m["details"].(map[string]any)
	return d
}

func (e *rtest) doCtx(ctx context.Context, method, path, tenantID string, body any) resp {
	e.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		require.NoError(e.t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", tenantID)
	userID := e.userA
	if tenantID == e.tenB {
		userID = e.userB
	}
	req.Header.Set("X-User-ID", userID)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	out := resp{Status: rec.Code, Raw: rec.Body.String()}
	require.NoError(e.t, json.Unmarshal(rec.Body.Bytes(), &out.Body), "response is not JSON: %s", out.Raw)
	for _, leak := range []string{"SQLSTATE", "violates", "duplicate key", "pgx"} {
		require.NotContains(e.t, out.Raw, leak, "response leaks a database message")
	}
	// Postgres "ERROR:" prefix (gateway codes such as DUPLICATE_ERROR: are fine).
	require.NotRegexp(e.t, `(^|[^A-Z_])ERROR:`, out.Raw, "response leaks a database message")
	return out
}

func (e *rtest) refund(tenantID, returnID string, body any) resp {
	e.t.Helper()
	return e.doCtx(context.Background(), http.MethodPost, "/admin/returns/"+returnID+"/refund", tenantID, body)
}

type refundState struct {
	id, status, providerRef, providerStatus, paymentID, failureReason string
	settled                                                           bool
	attempt                                                           int
	amount                                                            int64
}

func (e *rtest) refundRow(returnID string) refundState {
	e.t.Helper()
	var s refundState
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT id::text, status, provider_ref, COALESCE(provider_status,''), COALESCE(payment_id::text,''),
	    COALESCE(failure_reason,''), settled_at IS NOT NULL, attempt, amount FROM refunds WHERE return_id=$1`, returnID).
		Scan(&s.id, &s.status, &s.providerRef, &s.providerStatus, &s.paymentID, &s.failureReason, &s.settled, &s.attempt, &s.amount))
	return s
}

func (e *rtest) refundCount(returnID string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM refunds WHERE return_id=$1`, returnID).Scan(&n))
	return n
}

func (e *rtest) paymentRow(paymentID string) (string, int64) {
	e.t.Helper()
	var status string
	var refunded int64
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT status::text, refunded_amount FROM payments WHERE id=$1`, paymentID).Scan(&status, &refunded))
	return status, refunded
}

func (e *rtest) returnStatus(returnID string) string {
	e.t.Helper()
	var status string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT status FROM returns WHERE id=$1`, returnID).Scan(&status))
	return status
}

func (e *rtest) refundEvents(paymentID string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_events WHERE payment_id=$1 AND payload ? 'refundId'`, paymentID).Scan(&n))
	return n
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

// ---------------------------------------------------------------------------
// Cases

func TestAdminRefundFullRefund(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	d := res.data()
	require.Equal(t, "SUCCEEDED", d["status"])
	require.Equal(t, orderTotal, num(d["amount"]))
	require.Equal(t, "REFUNDED", d["paymentStatus"])
	require.Equal(t, orderTotal, num(d["refundedAmount"]))
	require.Equal(t, int64(0), num(d["refundableAmount"]))

	row := e.refundRow(s.ret)
	require.Equal(t, d["id"], row.id)
	require.Equal(t, "SUCCEEDED", row.status)
	require.True(t, row.settled)
	require.Equal(t, 0, row.attempt)
	require.Equal(t, s.payment, row.paymentID)
	require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "REFUNDED", status)
	require.Equal(t, orderTotal, refunded)
	require.Equal(t, 1, e.refundEvents(s.payment))
	require.Equal(t, []string{"rf-" + row.id + "-0"}, e.gw.keys())
	require.Equal(t, "inv-1", e.gw.requests[0].PaymentRef)
	require.Equal(t, s.order, e.gw.requests[0].OrderID)
}

func TestAdminRefundPartialThenSecondReturn(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	res := e.refund(e.tenA, s.ret, map[string]any{"amount": 40000, "reason": "one item damaged"})
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, "PARTIALLY_REFUNDED", res.data()["paymentStatus"])
	require.Equal(t, int64(60000), num(res.data()["refundableAmount"]))
	require.Equal(t, "one item damaged", e.gw.requests[0].Reason)
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "PARTIALLY_REFUNDED", status)
	require.Equal(t, int64(40000), refunded)

	second := e.addReturn(e.tenA, e.userA, s.order, "APPROVED")
	res = e.refund(e.tenA, second, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, int64(60000), num(res.data()["amount"]))
	require.Equal(t, "REFUNDED", res.data()["paymentStatus"])
	keys := e.gw.keys()
	require.Len(t, keys, 2)
	require.NotEqual(t, keys[0], keys[1])
	status, refunded = e.paymentRow(s.payment)
	require.Equal(t, "REFUNDED", status)
	require.Equal(t, orderTotal, refunded)
}

func TestAdminRefundRejectsOverRefund(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	require.Equal(t, http.StatusOK, e.refund(e.tenA, s.ret, map[string]any{"amount": 40000}).Status)
	second := e.addReturn(e.tenA, e.userA, s.order, "APPROVED")
	res := e.refund(e.tenA, second, map[string]any{"amount": 70000})
	require.Equal(t, http.StatusConflict, res.Status, res.Raw)
	require.Equal(t, "REFUND_EXCEEDS_REFUNDABLE", res.errCode())
	require.Equal(t, int64(70000), num(res.details()["requested"]))
	require.Equal(t, int64(60000), num(res.details()["refundable"]))
	require.Equal(t, 1, e.gw.callCount())
	require.Equal(t, 0, e.refundCount(second))
}

func TestAdminRefundRejectsBadAmount(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	for _, body := range []any{map[string]any{"amount": 0}, map[string]any{"amount": -5}, "{", map[string]any{"reason": strings.Repeat("x", 501)}} {
		res := e.refund(e.tenA, s.ret, body)
		require.Equal(t, http.StatusBadRequest, res.Status, res.Raw)
		require.Equal(t, "BAD_REQUEST", res.errCode())
	}
	res := e.doCtx(context.Background(), http.MethodPost, "/admin/returns/not-a-uuid/refund", e.tenA, nil)
	require.Equal(t, http.StatusBadRequest, res.Status)
	require.Equal(t, 0, e.gw.callCount())
	require.Equal(t, 0, e.refundCount(s.ret))
}

func TestAdminRefundRequiresApprovedReturn(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	e.exec(`UPDATE returns SET status='REQUESTED' WHERE id=$1`, s.ret)
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "REFUND_NOT_ALLOWED", res.errCode())
	e.exec(`UPDATE returns SET status='REFUNDED' WHERE id=$1`, s.ret)
	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "REFUND_ALREADY_ISSUED", res.errCode())
	require.Equal(t, 0, e.gw.callCount())
}

func TestAdminRefundTenantIsolation(t *testing.T) {
	e := newRTest(t)
	a := e.seed(e.tenA, e.userA, "fake")
	b := e.seed(e.tenB, e.userB, "fake")
	res := e.refund(e.tenA, b.ret, nil)
	require.Equal(t, http.StatusNotFound, res.Status)
	require.Equal(t, "APPROVED", e.returnStatus(b.ret))
	status, refunded := e.paymentRow(b.payment)
	require.Equal(t, "PAID", status)
	require.Zero(t, refunded)

	// A newer PAID payment row on A's order but owned by tenant B must never be used.
	var foreign string
	require.NoError(t, e.pool.QueryRow(context.Background(), `INSERT INTO payments(order_id,provider,status,intent_token,amount,tenant_id,created_at)
	    VALUES($1,'fake','PAID','inv-foreign',$2,$3,now()+interval '1 minute') RETURNING id::text`, a.order, orderTotal, e.tenB).Scan(&foreign))
	res = e.refund(e.tenA, a.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, a.payment, e.refundRow(a.ret).paymentID)
	require.Equal(t, "inv-1", e.gw.requests[0].PaymentRef)
	status, refunded = e.paymentRow(foreign)
	require.Equal(t, "PAID", status)
	require.Zero(t, refunded)
}

func TestAdminRefundNoPayment(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "")
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "PAYMENT_NOT_REFUNDABLE", res.errCode())
	require.Equal(t, 0, e.refundCount(s.ret))
}

func TestAdminRefundUnsupportedProvider(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "norefund")
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "REFUND_UNSUPPORTED", res.errCode())
	require.Equal(t, 0, e.refundCount(s.ret))
}

func TestAdminRefundDefiniteFailureRotatesKey(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	e.gw.set(payment.RefundResult{}, &payment.RefundError{Definite: true, Code: "INSUFFICIENT_BALANCE", Message: "balance too low"})
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
	require.Equal(t, "REFUND_FAILED", res.errCode())
	require.Equal(t, false, res.details()["retryable"])
	require.Contains(t, res.errMessage(), "INSUFFICIENT_BALANCE")
	row := e.refundRow(s.ret)
	require.Equal(t, res.details()["refundId"], row.id)
	require.Equal(t, "FAILED", row.status)
	require.Equal(t, "INSUFFICIENT_BALANCE", row.providerStatus)
	require.NotEmpty(t, row.failureReason)
	require.Empty(t, row.providerRef)
	require.False(t, row.settled)
	require.Equal(t, 0, row.attempt)
	require.Equal(t, "APPROVED", e.returnStatus(s.ret))
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "PAID", status)
	require.Zero(t, refunded)

	e.gw.set(payment.RefundResult{}, nil)
	// The fake gateway rejects a key it already answered definitively.
	_, err := e.gw.Refund(context.Background(), payment.RefundRequest{RefundKey: "rf-" + row.id + "-0", Amount: orderTotal})
	require.True(t, payment.IsDefiniteRefundFailure(err))
	require.Equal(t, "DUPLICATE_KEY", payment.RefundErrorCode(err))
	e.gw.mu.Lock()
	e.gw.requests = e.gw.requests[:1]
	e.gw.mu.Unlock()

	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, []string{"rf-" + row.id + "-0", "rf-" + row.id + "-1"}, e.gw.keys())
	row = e.refundRow(s.ret)
	require.Equal(t, 1, row.attempt)
	require.True(t, row.settled)
	require.Equal(t, 1, e.refundCount(s.ret))
	require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
}

func TestAdminRefundIndeterminateFailureKeepsKey(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	e.gw.set(payment.RefundResult{}, context.DeadlineExceeded)
	res := e.refund(e.tenA, s.ret, map[string]any{"reason": "late parcel"})
	require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
	require.Equal(t, true, res.details()["retryable"])
	row := e.refundRow(s.ret)
	require.Equal(t, "PENDING", row.status)
	require.Equal(t, "UNKNOWN", row.providerStatus)
	require.Empty(t, row.providerRef)
	require.Equal(t, 0, row.attempt)
	require.NotEmpty(t, row.failureReason)
	require.Equal(t, "APPROVED", e.returnStatus(s.ret))
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "PAID", status)
	require.Zero(t, refunded)

	// A different amount cannot be sent under a key whose outcome is unknown.
	res = e.refund(e.tenA, s.ret, map[string]any{"amount": 1000})
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "REFUND_IN_PROGRESS", res.errCode())
	require.Equal(t, orderTotal, num(res.details()["pendingAmount"]))
	require.Equal(t, 1, e.gw.callCount())

	e.gw.set(payment.RefundResult{}, nil)
	res = e.refund(e.tenA, s.ret, map[string]any{"reason": "a different reason"})
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	key := "rf-" + row.id + "-0"
	require.Equal(t, []string{key, key}, e.gw.keys())
	// Byte-identical resend: amount and the stored reason are unchanged.
	require.Equal(t, e.gw.requests[0], e.gw.requests[1])
	require.Equal(t, 0, e.refundRow(s.ret).attempt)
}

func TestAdminRefundUnknownOutcomeUsesLookup(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fakelookup")
		id := e.seedRefundRow(e.tenA, s, "PENDING", "", "UNKNOWN", "fakelookup")
		key := "rf-" + id + "-0"
		e.gw.lookupResults[key] = payment.RefundResult{ProviderRef: key, Status: payment.RefundStatusSucceeded}
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		require.Equal(t, 0, e.gw.callCount())
		require.Equal(t, 1, e.gw.lookups)
		row := e.refundRow(s.ret)
		require.True(t, row.settled)
		require.Equal(t, key, row.providerRef)
		status, refunded := e.paymentRow(s.payment)
		require.Equal(t, "REFUNDED", status)
		require.Equal(t, orderTotal, refunded)
	})
	t.Run("not found", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fakelookup")
		id := e.seedRefundRow(e.tenA, s, "PENDING", "", "UNKNOWN", "fakelookup")
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		require.Equal(t, 1, e.gw.lookups)
		require.Equal(t, []string{"rf-" + id + "-0"}, e.gw.keys())
	})
	t.Run("lookup error stays unknown", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fakelookup")
		e.seedRefundRow(e.tenA, s, "PENDING", "", "UNKNOWN", "fakelookup")
		e.gw.lookupErr = errors.New("status endpoint down")
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
		require.Equal(t, true, res.details()["retryable"])
		require.Equal(t, 0, e.gw.callCount())
		row := e.refundRow(s.ret)
		require.Equal(t, "PENDING", row.status)
		require.Equal(t, "UNKNOWN", row.providerStatus)
	})
}

func TestAdminRefundParallelRetriesCallGatewayOnce(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	e.seedRefundRow(e.tenA, s, "PENDING", "", "UNKNOWN", "fake")
	entered := make(chan struct{})
	release := make(chan struct{})
	e.gw.beforeReply = func(context.Context, payment.RefundRequest) error {
		close(entered)
		<-release
		return nil
	}
	first := make(chan resp, 1)
	go func() { first <- e.refund(e.tenA, s.ret, nil) }()
	<-entered
	second := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, second.Status, second.Raw)
	require.Equal(t, "REFUND_IN_PROGRESS", second.errCode())
	close(release)
	res := <-first
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, 1, e.gw.callCount())
}

func TestAdminRefundMidtransBodyStatus412LeavesRecordsUntouched(t *testing.T) {
	e := newRTest(t)
	var mu sync.Mutex
	var keys []string
	reply := `{"status_code":"412","status_message":"Merchant cannot modify the status of the transaction"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RefundKey string `json:"refund_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		keys = append(keys, r.URL.Path+" "+body.RefundKey)
		out := reply
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, out)
	}))
	defer srv.Close()
	e.handler.Providers["midtrans"] = payment.Midtrans{ServerKey: "k", BaseURL: srv.URL}
	s := e.seed(e.tenA, e.userA, "midtrans")
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
	require.Equal(t, false, res.details()["retryable"])
	require.Contains(t, res.errMessage(), "412")
	row := e.refundRow(s.ret)
	require.Equal(t, "FAILED", row.status)
	require.Empty(t, row.providerRef)
	require.False(t, row.settled)
	require.Equal(t, "APPROVED", e.returnStatus(s.ret))
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "PAID", status)
	require.Zero(t, refunded)
	require.Equal(t, 0, e.refundEvents(s.payment))

	mu.Lock()
	reply = `{"refund_key":"rf-` + row.id + `-1","status_code":"200","transaction_status":"refund"}`
	mu.Unlock()
	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	path := "/v2/" + s.order + "/refund "
	require.Equal(t, []string{path + "rf-" + row.id + "-0", path + "rf-" + row.id + "-1"}, keys)
	require.Equal(t, "rf-"+row.id+"-1", e.refundRow(s.ret).providerRef)
}

func TestAdminRefundXenditDuplicateNeverRotatesKey(t *testing.T) {
	e := newRTest(t)
	var mu sync.Mutex
	var posts []string
	postStatus, postBody := http.StatusBadGateway, `bad gateway`
	listBody := `{"data":[],"has_more":false}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, listBody)
			return
		}
		posts = append(posts, r.Header.Get("idempotency-key"))
		w.WriteHeader(postStatus)
		_, _ = io.WriteString(w, postBody)
	}))
	defer srv.Close()
	e.handler.Providers["xendit"] = payment.Xendit{SecretKey: "xnd", BaseURL: srv.URL}
	s := e.seed(e.tenA, e.userA, "xendit")

	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
	require.Equal(t, true, res.details()["retryable"])
	id := e.refundRow(s.ret).id
	key := "rf-" + id + "-0"

	// Lookup finds nothing, the resend is answered as a duplicate: still unknown, same key.
	mu.Lock()
	postStatus, postBody = http.StatusConflict, `{"error_code":"DUPLICATE_ERROR","message":"reference_id already used"}`
	mu.Unlock()
	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
	require.Equal(t, true, res.details()["retryable"])
	row := e.refundRow(s.ret)
	require.Equal(t, "PENDING", row.status)
	require.Equal(t, "UNKNOWN", row.providerStatus)
	require.Equal(t, 0, row.attempt)

	// The refund shows up in Xendit's list: reconciled without another POST.
	mu.Lock()
	listBody = `{"data":[{"id":"rfd-77","reference_id":"` + key + `","status":"SUCCEEDED"}],"has_more":false}`
	mu.Unlock()
	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, "rfd-77", res.data()["providerRef"])
	require.Equal(t, []string{key, key}, posts)
	require.Equal(t, 0, e.refundRow(s.ret).attempt)
}

func TestAdminRefundRecordFailureThenReconcile(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	e.gw.set(payment.RefundResult{ProviderRef: "rfd-x", Status: payment.RefundStatusSucceeded}, nil)
	e.gw.beforeReply = func(ctx context.Context, _ payment.RefundRequest) error {
		_, err := e.pool.Exec(ctx, `UPDATE payments SET refunded_amount=$2 WHERE id=$1`, s.payment, orderTotal)
		return err
	}
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusInternalServerError, res.Status, res.Raw)
	require.Equal(t, "REFUND_RECORD_FAILED", res.errCode())
	row := e.refundRow(s.ret)
	require.Equal(t, row.id, res.details()["refundId"])
	require.Equal(t, "rfd-x", res.details()["providerRef"])
	require.Equal(t, "SUCCEEDED", row.status)
	require.Equal(t, "rfd-x", row.providerRef)
	require.NotEmpty(t, row.providerStatus)
	require.False(t, row.settled)
	require.Equal(t, "APPROVED", e.returnStatus(s.ret))
	require.Equal(t, 0, e.refundEvents(s.payment))

	e.gw.beforeReply = nil
	e.exec(`UPDATE payments SET refunded_amount=0 WHERE id=$1`, s.payment)
	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, 1, e.gw.callCount())
	require.True(t, e.refundRow(s.ret).settled)
	require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "REFUNDED", status)
	require.Equal(t, orderTotal, refunded)
	require.Equal(t, 1, e.refundEvents(s.payment))
}

func TestAdminRefundReconcilesRegardlessOfPaymentStatus(t *testing.T) {
	t.Run("payment already REFUNDED by a webhook", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fake")
		e.seedRefundRow(e.tenA, s, "SUCCEEDED", "rfd-x", "SUCCEEDED", "fake")
		e.exec(`UPDATE payments SET status='REFUNDED' WHERE id=$1`, s.payment)
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		require.Equal(t, 0, e.gw.callCount())
		status, refunded := e.paymentRow(s.payment)
		require.Equal(t, "REFUNDED", status)
		require.Equal(t, orderTotal, refunded)
		require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
		require.True(t, e.refundRow(s.ret).settled)
	})
	t.Run("return moved to CANCELLED", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fake")
		e.seedRefundRow(e.tenA, s, "SUCCEEDED", "rfd-x", "SUCCEEDED", "fake")
		e.exec(`UPDATE returns SET status='CANCELLED' WHERE id=$1`, s.ret)
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
		require.Equal(t, 0, e.gw.callCount())
	})
}

func TestAdminReturnStatusRefusedWhileRefundUnsettled(t *testing.T) {
	e := newRTest(t)
	patch := func(returnID string) resp {
		return e.doCtx(context.Background(), http.MethodPatch, "/admin/returns/"+returnID, e.tenA, map[string]any{"status": "CANCELLED"})
	}
	s := e.seed(e.tenA, e.userA, "fake")
	id := e.seedRefundRow(e.tenA, s, "SUCCEEDED", "rfd-x", "SUCCEEDED", "fake")
	res := patch(s.ret)
	require.Equal(t, http.StatusConflict, res.Status, res.Raw)
	require.Equal(t, "RETURN_REFUND_PENDING", res.errCode())
	require.Equal(t, id, res.details()["refundId"])
	require.Equal(t, "APPROVED", e.returnStatus(s.ret))

	e.exec(`UPDATE refunds SET status='PENDING', provider_ref='', provider_status='UNKNOWN' WHERE id=$1`, id)
	require.Equal(t, "RETURN_REFUND_PENDING", patch(s.ret).errCode())

	e.exec(`UPDATE refunds SET status='FAILED', provider_status='INSUFFICIENT_BALANCE' WHERE id=$1`, id)
	res = patch(s.ret)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, "CANCELLED", e.returnStatus(s.ret))

	e.exec(`UPDATE returns SET status='REFUNDED' WHERE id=$1`, s.ret)
	res = patch(s.ret)
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "REFUND_ALREADY_ISSUED", res.errCode())

	// Tenant B cannot touch tenant A's return.
	res = e.doCtx(context.Background(), http.MethodPatch, "/admin/returns/"+s.ret, e.tenB, map[string]any{"status": "APPROVED"})
	require.Equal(t, http.StatusNotFound, res.Status)
}

func TestAdminRefundSettlesAfterRequestContextCanceled(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.gw.beforeReply = func(context.Context, payment.RefundRequest) error {
		cancel()
		return nil
	}
	res := e.doCtx(ctx, http.MethodPost, "/admin/returns/"+s.ret+"/refund", e.tenA, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.NoError(t, e.gw.ctxErrs[0], "gateway call must run on a detached context")
	row := e.refundRow(s.ret)
	require.Equal(t, "SUCCEEDED", row.status)
	require.True(t, row.settled)
	require.NotEmpty(t, row.providerRef)
	require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
	status, _ := e.paymentRow(s.payment)
	require.Equal(t, "REFUNDED", status)

	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, res.Status)
	require.Equal(t, "REFUND_ALREADY_ISSUED", res.errCode())
	require.Equal(t, 1, e.gw.callCount())
}

func TestAdminRefundSlowGatewayStillRecordsOutcome(t *testing.T) {
	restore := platform.SetRefundTimeoutsForTest(200*time.Millisecond, 5*time.Second)
	defer restore()

	t.Run("deadline reached: marked unknown", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fake")
		e.gw.beforeReply = func(ctx context.Context, _ payment.RefundRequest) error {
			<-ctx.Done()
			return ctx.Err()
		}
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusBadGateway, res.Status, res.Raw)
		require.Equal(t, true, res.details()["retryable"])
		row := e.refundRow(s.ret)
		require.Equal(t, "PENDING", row.status)
		require.Equal(t, "UNKNOWN", row.providerStatus, "bookkeeping must not run on the expired gateway context")
	})
	t.Run("success at the deadline: finalised", func(t *testing.T) {
		e := newRTest(t)
		s := e.seed(e.tenA, e.userA, "fake")
		e.gw.beforeReply = func(ctx context.Context, _ payment.RefundRequest) error {
			<-ctx.Done()
			return nil // the gateway answered just as the budget ran out
		}
		res := e.refund(e.tenA, s.ret, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		require.True(t, e.refundRow(s.ret).settled)
		require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
	})
}

func TestAdminRefundInProgressGuard(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	id := e.seedRefundRow(e.tenA, s, "PENDING", "", nil, "fake")
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusConflict, res.Status, res.Raw)
	require.Equal(t, "REFUND_IN_PROGRESS", res.errCode())
	require.Equal(t, 0, e.gw.callCount())

	e.exec(`UPDATE refunds SET updated_at = now() - interval '5 minutes' WHERE id=$1`, id)
	res = e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, []string{"rf-" + id + "-0"}, e.gw.keys())
}

func TestReturnReadsIncludeRefund(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	findReturn := func() map[string]any {
		res := e.doCtx(context.Background(), http.MethodGet, "/admin/returns", e.tenA, nil)
		require.Equal(t, http.StatusOK, res.Status, res.Raw)
		items, _ := res.Body["data"].([]any)
		for _, it := range items {
			m := it.(map[string]any)
			if m["id"] == s.ret {
				return m
			}
		}
		t.Fatalf("return %s not listed", s.ret)
		return nil
	}
	before := findReturn()
	require.Contains(t, before, "refund")
	require.Nil(t, before["refund"])
	get := e.doCtx(context.Background(), http.MethodGet, "/returns/"+s.ret, e.tenA, nil)
	require.Equal(t, http.StatusOK, get.Status, get.Raw)
	require.Nil(t, get.data()["refund"])

	res := e.refund(e.tenA, s.ret, map[string]any{"amount": 40000})
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	refundID := res.data()["id"]

	after := findReturn()["refund"].(map[string]any)
	require.Equal(t, refundID, after["id"])
	require.Equal(t, "SUCCEEDED", after["status"])
	require.Equal(t, int64(40000), num(after["amount"]))
	require.Equal(t, res.data()["providerRef"], after["providerRef"])
	require.Equal(t, true, after["settled"])
	require.Equal(t, int64(0), num(after["attempt"]))
	require.Contains(t, after, "providerStatus")

	get = e.doCtx(context.Background(), http.MethodGet, "/returns/"+s.ret, e.tenA, nil)
	customer := get.data()["refund"].(map[string]any)
	require.Equal(t, refundID, customer["id"])
	require.Equal(t, true, customer["settled"])
	require.NotContains(t, customer, "providerStatus", "gateway diagnostics are admin-only")
}

func TestAdminRefundPendingGatewayStatus(t *testing.T) {
	e := newRTest(t)
	s := e.seed(e.tenA, e.userA, "fake")
	e.gw.set(payment.RefundResult{ProviderRef: "rfd-p", Status: payment.RefundStatusPending}, nil)
	res := e.refund(e.tenA, s.ret, nil)
	require.Equal(t, http.StatusOK, res.Status, res.Raw)
	require.Equal(t, "PENDING", res.data()["status"])
	row := e.refundRow(s.ret)
	require.Equal(t, "PENDING", row.status)
	require.True(t, row.settled)
	require.Equal(t, "REFUNDED", e.returnStatus(s.ret))
	status, refunded := e.paymentRow(s.payment)
	require.Equal(t, "REFUNDED", status)
	require.Equal(t, orderTotal, refunded)
}
