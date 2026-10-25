package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/reservation"
	"github.com/noah-isme/backend-toko/internal/tenant"
	fx "github.com/noah-isme/backend-toko/internal/testutil/pgfixture"
)

// Run with: TEST_DATABASE_URL=<migrated db> go test ./internal/payment/ -run Integration

// stubProvider accepts any body of the form {"order_id","status","amount","n"}.
type stubProvider struct{}

func (stubProvider) CreateIntent(context.Context, payment.IntentRequest) (payment.IntentResponse, error) {
	return payment.IntentResponse{}, errors.New("not used")
}

func (stubProvider) VerifyWebhook(_ *http.Request, body []byte) (payment.WebhookVerifyResult, error) {
	var in struct {
		OrderID string `json:"order_id"`
		Status  string `json:"status"`
		Amount  int64  `json:"amount"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return payment.WebhookVerifyResult{}, err
	}
	return payment.WebhookVerifyResult{Valid: true, OrderID: in.OrderID, Status: in.Status, Amount: in.Amount, ProviderPayload: body}, nil
}

type recordingMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *recordingMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+subject)
	return nil
}

func (m *recordingMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type webhookEnv struct {
	d       *fx.DB
	h       payment.Webhook
	router  http.Handler
	redis   *miniredis.Miniredis
	mailer  *recordingMailer
	appName string
	tenant  pgtype.UUID
	user    pgtype.UUID
	email   string
	product pgtype.UUID
	variant pgtype.UUID
}

func newWebhookEnv(t *testing.T) *webhookEnv {
	d := fx.Open(t)
	e := &webhookEnv{d: d, mailer: &recordingMailer{}, appName: "toko-webhook-test-" + fx.RandSuffix()}
	cfg, err := pgxpool.ParseConfig(d.URL)
	require.NoError(t, err)
	cfg.ConnConfig.RuntimeParams["application_name"] = e.appName
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	e.redis = mr
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	bus := &events.Bus{
		Store: d.Q,
		// The real EmailNotifier with the production toggle map: proves
		// payment.late never reaches the customer.
		Notifiers: []events.Notifier{notify.EmailNotifier{Mail: e.mailer, Enabled: true, TopicToggles: map[string]bool{}}},
	}
	e.h = payment.Webhook{
		Q:         dbgen.New(pool),
		Pool:      pool,
		Providers: map[string]payment.Provider{"stub": stubProvider{}},
		Replay:    client,
		ReplayTTL: time.Minute,
		Events:    bus,
	}
	r := chi.NewRouter()
	r.Post("/webhooks/payment/{provider}", e.h.Handle)
	r.With(func(next http.Handler) http.Handler {
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
	}).Post("/admin/orders/{id}/confirm-payment", e.h.AdminConfirmPayment)
	e.router = r

	e.tenant = d.Tenant()
	e.user, e.email = d.User()
	e.product, e.variant = d.Variant(e.tenant, 10)
	return e
}

func (e *webhookEnv) order(status dbgen.OrderStatus, paymentStatus dbgen.PaymentStatus, resStatus dbgen.InventoryReservationStatus) fx.Order {
	return e.d.Order(fx.OrderOpts{
		TenantID:          e.tenant,
		UserID:            e.user,
		Status:            status,
		PaymentExpiresAt:  time.Now().Add(-time.Hour),
		PaymentStatus:     paymentStatus,
		ReservationStatus: resStatus,
		Lines:             []fx.Line{{ProductID: e.product, VariantID: e.variant, Qty: 2, FlashSale: true}},
	})
}

func (e *webhookEnv) post(t *testing.T, orderID pgtype.UUID, status string, n int) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"order_id": fx.UUIDString(orderID), "status": status, "amount": 10000, "n": n})
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/webhooks/payment/stub", bytes.NewReader(body)))
	return rec.Code
}

func (e *webhookEnv) paymentEvents(paymentID pgtype.UUID, status string) int {
	return e.d.Int(`SELECT count(*) FROM payment_events WHERE payment_id = $1 AND status = $2`, paymentID, status)
}

func TestWebhookIntegration_PaidPendingOrderStillSettles(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "PAID", 1))
	require.Equal(t, "PAID", e.d.OrderStatus(o.ID))
	require.Equal(t, "PAID", e.d.PaymentStatus(o.PaymentID))
	require.Equal(t, "COMMITTED", e.d.ReservationStatus(o.ReservationIDs[0]))
	require.Equal(t, "COMMITTED", e.d.Str(`SELECT status FROM flash_sale_order_items WHERE order_id = $1`, o.ID))
	require.Equal(t, 8, e.d.Stock(e.variant))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicOrderPaid))
	require.Equal(t, 1, e.mailer.count())

	// Midtrans sends capture then settlement: the second PAID changes nothing.
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "PAID", 2))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicOrderPaid))
	require.Equal(t, 1, e.paymentEvents(o.PaymentID, "PAID"))
}

func TestWebhookIntegration_LatePaymentKeepsOrderExpired(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusEXPIRED, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRELEASED)
	stock := e.d.Stock(e.variant)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "PAID", 1))
	require.Equal(t, "EXPIRED", e.d.OrderStatus(o.ID))
	require.Equal(t, "PAID", e.d.PaymentStatus(o.PaymentID))
	require.Equal(t, 1, e.paymentEvents(o.PaymentID, "PAID"))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicPaymentLate))
	require.Zero(t, e.d.Events(o.ID, events.TopicOrderPaid))
	require.Equal(t, stock, e.d.Stock(e.variant))
	require.Equal(t, "RELEASED", e.d.ReservationStatus(o.ReservationIDs[0]))

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(e.d.Str(`SELECT payload::text FROM domain_events WHERE aggregate_id = $1 AND topic = $2`, o.ID, events.TopicPaymentLate)), &payload))
	require.Equal(t, "EXPIRED", payload["orderStatus"])
	require.Equal(t, fx.UUIDString(e.tenant), payload["tenantId"])
	require.NotContains(t, payload, "email")
	require.Zero(t, e.mailer.count(), "payment.late must not email the customer")
}

func TestWebhookIntegration_LatePaidTwiceEmitsOnce(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusEXPIRED, dbgen.PaymentStatusEXPIRED, dbgen.InventoryReservationStatusRELEASED)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "PAID", 1))
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "SETTLED", 2))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicPaymentLate))
	require.Zero(t, e.d.Events(o.ID, events.TopicOrderPaid))
	require.Equal(t, 1, e.paymentEvents(o.PaymentID, "PAID"))
}

func TestWebhookIntegration_ProviderExpiredAfterWorkerExpiryEmitsNothing(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusEXPIRED, dbgen.PaymentStatusEXPIRED, dbgen.InventoryReservationStatusRELEASED)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "EXPIRED", 1))
	require.Zero(t, e.d.AllEvents(o.ID))
	require.Zero(t, e.d.Int(`SELECT count(*) FROM payment_events WHERE payment_id = $1`, o.PaymentID))
}

func TestWebhookIntegration_ProviderExpiredMovesOrderToExpired(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	require.Equal(t, 8, e.d.Stock(e.variant))
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "EXPIRED", 1))
	require.Equal(t, "EXPIRED", e.d.OrderStatus(o.ID))
	require.Equal(t, "RELEASED", e.d.ReservationStatus(o.ReservationIDs[0]))
	require.Equal(t, 10, e.d.Stock(e.variant))
	require.Equal(t, 0, e.d.Int(`SELECT sold_count FROM flash_sale_items WHERE id = $1`, o.FlashItemIDs[0]))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicPaymentExpired))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicOrderCanceled))

	// A provider FAILED callback afterwards is a no-op.
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "FAILED", 2))
	require.Equal(t, 2, e.d.AllEvents(o.ID))
}

func TestWebhookIntegration_ExpiredCallbackDoesNotOverwritePaid(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusPAID, dbgen.PaymentStatusPAID, dbgen.InventoryReservationStatusCOMMITTED)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "EXPIRED", 1))
	require.Equal(t, "PAID", e.d.PaymentStatus(o.PaymentID))
	require.Equal(t, "PAID", e.d.OrderStatus(o.ID))
	require.Zero(t, e.d.AllEvents(o.ID))
}

func TestWebhookIntegration_ReplayKeyDeletedOnFailure(t *testing.T) {
	e := newWebhookEnv(t)
	// An order without a payment row: 404 PAYMENT_NOT_FOUND after SETNX.
	bare := e.d.Order(fx.OrderOpts{TenantID: e.tenant, UserID: e.user, Lines: []fx.Line{{ProductID: e.product, VariantID: e.variant, Qty: 1}}})
	require.Equal(t, http.StatusNotFound, e.post(t, bare.ID, "PAID", 1))
	require.Empty(t, e.redis.Keys(), "replay key must be dropped after a failed delivery")
	require.Equal(t, http.StatusNotFound, e.post(t, bare.ID, "PAID", 1), "a retry must be processed, not rejected as a replay")

	o := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "PAID", 7))
	require.Equal(t, http.StatusConflict, e.post(t, o.ID, "PAID", 7), "an identical successful delivery is a replay")
}

// The expiry job holds the order row lock; the PAID webhook must wait for it
// (observed as a lock wait in pg_stat_activity), then take the late-payment
// path. Neither side may deadlock.
func TestWebhookIntegration_ConcurrentExpiryAndPaid(t *testing.T) {
	e := newWebhookEnv(t)
	d := e.d
	o := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	ctx := context.Background()

	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	qtx := d.Q.WithTx(tx)
	_, err = qtx.LockPendingOrderForExpiry(ctx, dbgen.LockPendingOrderForExpiryParams{ID: o.ID, TenantID: e.tenant})
	require.NoError(t, err)

	codeCh := make(chan int, 1)
	go func() { codeCh <- e.post(t, o.ID, "PAID", 1) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if d.Int(`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock'`, e.appName) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("webhook never waited on the order row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	_, err = qtx.UpdateOrderStatusIfAllowed(ctx, dbgen.UpdateOrderStatusIfAllowedParams{ID: o.ID, Status: dbgen.OrderStatusEXPIRED})
	require.NoError(t, err)
	_, err = qtx.ExpirePendingPaymentsByOrder(ctx, o.ID)
	require.NoError(t, err)
	_, err = reservation.ReleaseOrder(ctx, qtx, o.ID, dbgen.InventoryReservationStatusRELEASED)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	select {
	case code := <-codeCh:
		require.Equal(t, http.StatusNoContent, code)
	case <-time.After(10 * time.Second):
		t.Fatal("webhook did not finish")
	}
	require.Equal(t, "EXPIRED", d.OrderStatus(o.ID))
	require.Equal(t, "PAID", d.PaymentStatus(o.PaymentID))
	require.Equal(t, 1, d.Events(o.ID, events.TopicPaymentLate))
	require.Zero(t, d.Events(o.ID, events.TopicOrderPaid))
	require.Equal(t, 10, d.Stock(e.variant))
}

func (e *webhookEnv) confirm(t *testing.T, orderID pgtype.UUID, tenantID pgtype.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/admin/orders/"+fx.UUIDString(orderID)+"/confirm-payment", strings.NewReader(body))
	req.Header.Set("X-Tenant-ID", fx.UUIDString(tenantID))
	req.Header.Set("X-User-ID", fx.UUIDString(e.user))
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func TestWebhookIntegration_AdminConfirmPaymentSettlesManualTransfer(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	e.d.Proof(o.ID, e.user, time.Now().Add(-time.Hour))

	rec := e.confirm(t, o.ID, e.tenant, `{"note":"BCA transfer verified"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "PAID", body.Data["orderStatus"])
	require.Equal(t, "PAID", body.Data["paymentStatus"])
	require.Equal(t, fx.UUIDString(o.PaymentID), body.Data["paymentId"])

	require.Equal(t, "PAID", e.d.OrderStatus(o.ID))
	require.Equal(t, "PAID", e.d.PaymentStatus(o.PaymentID))
	require.Equal(t, "COMMITTED", e.d.ReservationStatus(o.ReservationIDs[0]))
	require.Equal(t, "COMMITTED", e.d.Str(`SELECT status FROM flash_sale_order_items WHERE order_id = $1`, o.ID))
	require.Equal(t, 8, e.d.Stock(e.variant))
	require.Equal(t, "admin", e.d.Str(`SELECT payload->>'source' FROM payment_events WHERE payment_id = $1 AND status = 'PAID'`, o.PaymentID))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicOrderPaid))

	// Confirming again, or confirming a provider PAID afterwards, changes nothing.
	require.Equal(t, http.StatusConflict, e.confirm(t, o.ID, e.tenant, ``).Code)
	require.Equal(t, http.StatusNoContent, e.post(t, o.ID, "PAID", 1))
	require.Equal(t, 1, e.d.Events(o.ID, events.TopicOrderPaid))
}

func TestWebhookIntegration_AdminConfirmPaymentRejectsExpiredAndForeignOrders(t *testing.T) {
	e := newWebhookEnv(t)
	expired := e.order(dbgen.OrderStatusEXPIRED, dbgen.PaymentStatusEXPIRED, dbgen.InventoryReservationStatusRELEASED)
	rec := e.confirm(t, expired.ID, e.tenant, ``)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "EXPIRED", e.d.OrderStatus(expired.ID))

	pending := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	other := e.d.Tenant()
	require.Equal(t, http.StatusNotFound, e.confirm(t, pending.ID, other, ``).Code)
	require.Equal(t, "PENDING_PAYMENT", e.d.OrderStatus(pending.ID))

	require.Equal(t, http.StatusBadRequest, e.confirm(t, pending.ID, e.tenant, `{"note":"`+strings.Repeat("x", 501)+`"}`).Code)
}

func TestUploadProofRejectsNonPendingOrderIntegration(t *testing.T) {
	e := newWebhookEnv(t)
	h := &payment.Handler{Q: e.d.Q, Pool: e.d.Pool}
	r := chi.NewRouter()
	r.Post("/payments/{orderId}/proof", func(w http.ResponseWriter, req *http.Request) {
		h.UploadProof(w, req.WithContext(common.WithUserID(req.Context(), fx.UUIDString(e.user))))
	})
	upload := func(orderID pgtype.UUID) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("proof", "proof.png")
		require.NoError(t, err)
		_, _ = part.Write([]byte("png"))
		require.NoError(t, mw.Close())
		req := httptest.NewRequest(http.MethodPost, "/payments/"+fx.UUIDString(orderID)+"/proof", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	expired := e.order(dbgen.OrderStatusEXPIRED, dbgen.PaymentStatusEXPIRED, dbgen.InventoryReservationStatusRELEASED)
	require.Equal(t, http.StatusConflict, upload(expired.ID))
	pending := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusPENDING, dbgen.InventoryReservationStatusRESERVED)
	require.Equal(t, http.StatusCreated, upload(pending.ID))
	require.Equal(t, 1, e.d.Int(`SELECT count(*) FROM payment_proofs WHERE order_id = $1`, pending.ID))
}

// expiringProvider simulates the order being expired while the provider call
// is in flight.
type expiringProvider struct {
	d       *fx.DB
	orderID pgtype.UUID
}

func (p expiringProvider) CreateIntent(ctx context.Context, req payment.IntentRequest) (payment.IntentResponse, error) {
	if _, err := p.d.Pool.Exec(ctx, `UPDATE orders SET status = 'EXPIRED' WHERE id = $1`, p.orderID); err != nil {
		return payment.IntentResponse{}, err
	}
	return payment.IntentResponse{Provider: "stub", Token: "tok", RedirectURL: "https://pay.example/tok", ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}, nil
}

func (expiringProvider) VerifyWebhook(*http.Request, []byte) (payment.WebhookVerifyResult, error) {
	return payment.WebhookVerifyResult{}, errors.New("not used")
}

func TestCreateIntentIntegration_RefusesOrderExpiredDuringProviderCall(t *testing.T) {
	e := newWebhookEnv(t)
	o := e.order(dbgen.OrderStatusPENDINGPAYMENT, dbgen.PaymentStatusEXPIRED, dbgen.InventoryReservationStatusRESERVED)
	svc := &payment.Service{Q: e.d.Q, Pool: e.d.Pool, Provider: expiringProvider{d: e.d, orderID: o.ID}, IntentTTL: 15 * time.Minute}
	_, err := svc.CreateIntent(context.Background(), fx.UUIDString(o.ID), 0, "bank_transfer", "")
	require.ErrorIs(t, err, payment.ErrOrderNotPayable)
	require.Equal(t, 1, e.d.Int(`SELECT count(*) FROM payments WHERE order_id = $1`, o.ID), "no live payment may be stored for an expired order")

	_, err = svc.CreateIntent(context.Background(), fx.UUIDString(o.ID), 0, "bank_transfer", "")
	require.ErrorIs(t, err, payment.ErrOrderNotPayable)
}
