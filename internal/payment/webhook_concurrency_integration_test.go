package payment_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/catalog"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

// stubWebhookProvider accepts any callback and reports PAID for the order id in the body.
type stubWebhookProvider struct{}

func (stubWebhookProvider) CreateIntent(context.Context, payment.IntentRequest) (payment.IntentResponse, error) {
	return payment.IntentResponse{}, nil
}

func (stubWebhookProvider) VerifyWebhook(_ *http.Request, body []byte) (payment.WebhookVerifyResult, error) {
	return payment.WebhookVerifyResult{Valid: true, OrderID: strings.TrimSpace(string(body)), Status: "PAID", ProviderPayload: []byte(`{"stub":true}`)}, nil
}

// slowVoucherSettler stands in for the voucher service's network/DB latency: it waits,
// then settles through the real voucher.Service, which runs on the pool (not the
// webhook transaction) exactly like cmd/api/main.go wires it.
type slowVoucherSettler struct {
	delay time.Duration
	inner *voucher.Service
}

func (s slowVoucherSettler) Settle(ctx context.Context, tenantID pgtype.UUID, code string, orderID, userID pgtype.UUID, amount int64) error {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.inner.Settle(ctx, tenantID, code, orderID, userID, amount)
}

func randHex() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TestIntegrationPaidWebhooksForOneProductDoNotSerialize covers the payment-path
// availability requirement behind the popularity counter design: N concurrent PAID
// webhooks for orders that all contain the same product, on a pool of exactly N
// connections (DB_MAX_OPEN_CONNS defaults to 5), one of them settling a voucher on the
// pool while its transaction is open. Settlement must not take any shared per-product
// row lock; if it did (e.g. a product_sales upsert trigger on orders), the N-1
// voucher-less webhooks would park on that lock holding every pooled connection and the
// voucher webhook could never acquire one, stalling the API. The counter is instead
// recomputed by catalog.PopularityRefresher, verified at the end.
func TestIntegrationPaidWebhooksForOneProductDoNotSerialize(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	const n = 5
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	require.NoError(t, err)
	cfg.MaxConns = n
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var tenantID, productID, voucherCode string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO tenants(slug,name) VALUES($1,'IT webhook') RETURNING id::text`, "it-wh-"+randHex()).Scan(&tenantID))
	t.Cleanup(func() {
		c := context.Background()
		for _, stmt := range []string{
			`DELETE FROM orders WHERE tenant_id = $1`,
			`DELETE FROM vouchers WHERE tenant_id = $1`,
			`DELETE FROM products WHERE tenant_id = $1`,
			`DELETE FROM tenants WHERE id = $1`,
		} {
			if _, err := pool.Exec(c, stmt, tenantID); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	slug := "it-wh-prod-" + randHex()
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products(title,slug,price,tenant_id) VALUES('Hot item',$1,100000,$2) RETURNING id::text`, slug, tenantID).Scan(&productID))
	voucherCode = "ITWH" + strings.ToUpper(randHex())
	_, err = pool.Exec(ctx, `INSERT INTO vouchers(code,value,tenant_id) VALUES($1,10000,$2)`, voucherCode, tenantID)
	require.NoError(t, err)

	orderIDs := make([]string, n)
	for i := 0; i < n; i++ {
		var code any
		if i == 0 {
			code = voucherCode
		}
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO orders(tenant_id,status,pricing_subtotal,pricing_discount,pricing_total,applied_voucher_code)
			 VALUES($1,'PENDING_PAYMENT',100000,0,100000,$2) RETURNING id::text`, tenantID, code).Scan(&orderIDs[i]))
		_, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'Hot item',$3,2,100000,200000)`,
			orderIDs[i], productID, slug)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO payments(order_id,provider,status,amount,tenant_id) VALUES($1,'stub','PENDING',100000,$2)`, orderIDs[i], tenantID)
		require.NoError(t, err)
	}

	queries := dbgen.New(pool)
	h := payment.Webhook{
		Q:         queries,
		Pool:      pool,
		Providers: map[string]payment.Provider{"stub": stubWebhookProvider{}},
		Voucher:   slowVoucherSettler{delay: 300 * time.Millisecond, inner: &voucher.Service{Q: queries}},
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	codes := make([]int, n)
	bodies := make([]string, n)
	var wg sync.WaitGroup
	fire := func(i int) {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/webhook/stub", strings.NewReader(orderIDs[i]))
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("provider", "stub")
		req = req.WithContext(context.WithValue(reqCtx, chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		h.Handle(rec, req)
		codes[i] = rec.Code
		bodies[i] = rec.Body.String()
	}
	started := time.Now()
	wg.Add(n)
	go fire(0) // the voucher webhook enters its transaction first...
	time.Sleep(50 * time.Millisecond)
	for i := 1; i < n; i++ { // ...and the others arrive while it sits in Voucher.Settle.
		go fire(i)
	}
	wg.Wait()
	elapsed := time.Since(started)

	for i, code := range codes {
		require.Equal(t, http.StatusNoContent, code, "webhook %d (elapsed %s): %s", i, elapsed, bodies[i])
	}
	require.Less(t, elapsed, 10*time.Second, "paid webhooks for one product must not stall the pool")

	var paid int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE tenant_id = $1 AND status = 'PAID'`, tenantID).Scan(&paid))
	require.Equal(t, n, paid)
	var usages int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM voucher_usages vu JOIN vouchers v ON v.id = vu.voucher_id WHERE v.code = $1`, voucherCode).Scan(&usages))
	require.Equal(t, 1, usages)

	// Nothing on the payment path touches product_sales; the refresher derives it.
	var tenantUUID pgtype.UUID
	require.NoError(t, tenantUUID.Scan(tenantID))
	var before int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM product_sales WHERE tenant_id = $1`, tenantID).Scan(&before))
	require.Zero(t, before)
	refreshed, _, _, err := catalog.PopularityRefresher{Pool: pool}.RefreshTenant(ctx, tenantUUID)
	require.NoError(t, err)
	require.True(t, refreshed)
	var units int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT units_sold FROM product_sales WHERE product_id = $1`, productID).Scan(&units))
	require.EqualValues(t, 2*n, units)
}
