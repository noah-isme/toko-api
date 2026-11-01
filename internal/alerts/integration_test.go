package alerts_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/alerts"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// These tests run the alert triggers, dispatcher and HTTP handlers against a
// real Postgres with all migrations applied. Opt-in via TEST_DATABASE_URL.

type captureNotifier struct {
	mu     sync.Mutex
	events []dbgen.DomainEvent
}

func (c *captureNotifier) Notify(_ context.Context, ev dbgen.DomainEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
	return nil
}

func (c *captureNotifier) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

type env struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	tenA    string
	tenB    string
	users   []string
	router  http.Handler
	capture *captureNotifier
}

func rnd(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func newEnv(t *testing.T) *env {
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

	e := &env{t: t, ctx: ctx, pool: pool, q: dbgen.New(pool), capture: &captureNotifier{}}
	e.tenA = e.insertTenant()
	e.tenB = e.insertTenant()
	// Drain leftovers from earlier runs so per-test assertions are exact.
	_, err = pool.Exec(ctx, `UPDATE product_alert_events SET processed_at = now() WHERE processed_at IS NULL`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE product_alert_deliveries SET failed_at = now() WHERE delivered_at IS NULL AND failed_at IS NULL`)
	require.NoError(t, err)
	t.Cleanup(func() {
		c := context.Background()
		tenants := []string{e.tenA, e.tenB}
		for _, stmt := range []string{
			`DELETE FROM product_alert_deliveries WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM domain_events WHERE topic LIKE 'alert.%' AND payload->>'tenantId' = ANY($1::text[])`,
			`DELETE FROM webhook_endpoints WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM product_alert_events WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM notifications WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM product_alert_subscriptions WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM tenants WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(c, stmt, tenants); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if _, err := pool.Exec(c, `DELETE FROM users WHERE id = ANY($1::uuid[])`, e.users); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})

	h := &alerts.Handler{Svc: &alerts.Service{Q: e.q}}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			c := req.Context()
			if id := req.Header.Get("X-Tenant-ID"); id != "" {
				c = tenant.WithTenant(c, id)
			}
			if id := req.Header.Get("X-User-ID"); id != "" {
				c = common.WithUserID(c, id)
			}
			next.ServeHTTP(w, req.WithContext(c))
		})
	})
	r.Get("/alerts", h.List)
	r.Post("/alerts", h.Subscribe)
	r.Delete("/alerts/{id}", h.Unsubscribe)
	e.router = r
	return e
}

func (e *env) dispatcher(maxSubs, batch int) *alerts.Dispatcher {
	return &alerts.Dispatcher{
		Pool:             e.pool,
		Q:                e.q,
		MaxSubscriptions: maxSubs,
		BatchSize:        batch,
		Currency:         "IDR",
		Retention:        -1, // pruning is exercised explicitly
		OnError:          func(err error) { e.t.Errorf("dispatcher error: %v", err) },
	}
}

func (e *env) mailer(notifiers ...events.Notifier) *alerts.Mailer {
	if len(notifiers) == 0 {
		notifiers = []events.Notifier{e.capture}
	}
	return &alerts.Mailer{
		Q:           e.q,
		Events:      &events.Bus{Store: e.q, Notifiers: notifiers},
		SendTimeout: time.Second,
		Retention:   -1,
	}
}

// drainMail runs mailer passes until none are due and returns deliveries.
func (e *env) drainMail(m *alerts.Mailer) int {
	e.t.Helper()
	delivered := 0
	for i := 0; i < 100; i++ {
		res, err := m.Flush(e.ctx)
		require.NoError(e.t, err)
		delivered += res.Delivered
		if res.Attempted == 0 {
			return delivered
		}
	}
	e.t.Fatal("drainMail did not converge")
	return delivered
}

func (e *env) insertTenant() string {
	var id string
	require.NoError(e.t, e.pool.QueryRow(e.ctx, `INSERT INTO tenants(slug,name) VALUES($1,'Alerts IT') RETURNING id::text`, rnd("it-alerts")).Scan(&id))
	return id
}

func (e *env) insertUser() (id, email string) {
	email = rnd("buyer") + "@example.com"
	require.NoError(e.t, e.pool.QueryRow(e.ctx, `INSERT INTO users(name,email,password_hash) VALUES('IT Buyer',$1,'x') RETURNING id::text`, email).Scan(&id))
	e.users = append(e.users, id)
	return id, email
}

func (e *env) insertProduct(tenantID, title string, price int64, inStock bool) string {
	var id string
	require.NoError(e.t, e.pool.QueryRow(e.ctx,
		`INSERT INTO products(title,slug,price,in_stock,tenant_id) VALUES($1,$2,$3,$4,$5) RETURNING id::text`,
		title, rnd("it-alert-prod"), price, inStock, tenantID).Scan(&id))
	return id
}

func (e *env) insertVariant(productID string, price int64, stock int32) string {
	var id string
	require.NoError(e.t, e.pool.QueryRow(e.ctx,
		`INSERT INTO product_variants(product_id,price,stock,attributes) VALUES($1,$2,$3,'{}') RETURNING id::text`,
		productID, price, stock).Scan(&id))
	return id
}

func (e *env) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(e.ctx, sql, args...)
	require.NoError(e.t, err)
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(e.ctx, sql, args...).Scan(&n))
	return n
}

func (e *env) do(method, path, tenantID, userID string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(e.t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("X-User-ID", userID)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// subscribe calls POST /alerts and returns the status and subscription id.
func (e *env) subscribe(tenantID, userID string, body map[string]any) (int, string) {
	e.t.Helper()
	rec := e.do(http.MethodPost, "/alerts", tenantID, userID, body)
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out.Data.ID
}

func (e *env) flushAll(d *alerts.Dispatcher) alerts.Result {
	e.t.Helper()
	var total alerts.Result
	for i := 0; i < 100; i++ {
		res, err := d.Flush(e.ctx)
		require.NoError(e.t, err)
		total.Events += res.Events
		total.Claimed += res.Claimed
		total.Notified += res.Notified
		total.Queued += res.Queued
		if !res.More {
			return total
		}
	}
	e.t.Fatal("flushAll did not converge")
	return total
}

func (e *env) notifications(userID, kind string) int {
	return e.count(`SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, userID, kind)
}

func (e *env) domainEvents(subID string) int {
	return e.count(`SELECT count(*) FROM domain_events WHERE aggregate_id=$1 AND topic LIKE 'alert.%'`, subID)
}

func TestIntegrationBackInStockFiresOnce(t *testing.T) {
	e := newEnv(t)
	user, email := e.insertUser()
	prod := e.insertProduct(e.tenA, "Kaos Hitam", 249000, true)
	variant := e.insertVariant(prod, 249000, 0)

	code, sub := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "variantId": variant, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusCreated, code)

	// (b) a non-transition change produces no event.
	other := e.insertVariant(e.insertProduct(e.tenA, "Other", 1000, true), 1000, 2)
	before := e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1`, other)
	e.exec(`UPDATE product_variants SET stock=7 WHERE id=$1`, other)
	require.Equal(t, before, e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1`, other))

	e.exec(`UPDATE product_variants SET stock=5 WHERE id=$1`, variant)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1 AND alert_type='BACK_IN_STOCK'`, variant))

	d := e.dispatcher(0, 0)
	res := e.flushAll(d)
	require.Equal(t, 1, res.Notified)
	require.Equal(t, 1, res.Queued)
	require.Equal(t, 0, e.capture.count(), "the dispatcher never sends email itself")
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_deliveries pd JOIN domain_events de ON de.id=pd.event_id WHERE de.aggregate_id=$1 AND pd.tenant_id=$2 AND pd.delivered_at IS NULL`, sub, e.tenA))
	require.Equal(t, 1, e.drainMail(e.mailer()))
	require.Equal(t, 1, e.capture.count())
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_deliveries pd JOIN domain_events de ON de.id=pd.event_id WHERE de.aggregate_id=$1 AND pd.delivered_at IS NOT NULL AND pd.attempts=1`, sub))
	require.Equal(t, 1, e.notifications(user, alerts.KindBackInStock))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE id=$1 AND notified_at IS NOT NULL`, sub))
	require.Equal(t, 1, e.domainEvents(sub))

	var payload map[string]any
	var raw []byte
	require.NoError(t, e.pool.QueryRow(e.ctx, `SELECT payload FROM domain_events WHERE aggregate_id=$1`, sub).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, email, payload["email"])
	require.Equal(t, "Kaos Hitam sudah tersedia kembali.", payload["message"])

	var body string
	require.NoError(t, e.pool.QueryRow(e.ctx, `SELECT body FROM notifications WHERE user_id=$1`, user).Scan(&body))
	require.Equal(t, "Kaos Hitam sudah tersedia kembali.", body)

	// Second Flush has nothing to do.
	res2, err := d.Flush(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 0, res2.Events)
	require.False(t, res2.More)

	// Going out of stock and back again does not re-fire (consumed).
	e.exec(`UPDATE product_variants SET stock=0 WHERE id=$1`, variant)
	e.exec(`UPDATE product_variants SET stock=3 WHERE id=$1`, variant)
	e.flushAll(d)
	require.Equal(t, 1, e.notifications(user, alerts.KindBackInStock))

	// (f) re-subscribe re-arms: 200, notified_at cleared, fires on next 0 -> >0.
	e.exec(`UPDATE product_variants SET stock=0 WHERE id=$1`, variant)
	code, sub2 := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "variantId": variant, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, sub, sub2)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE id=$1 AND notified_at IS NULL`, sub))

	// (g) a stock release from 0 (order cancel path) triggers the event.
	require.NoError(t, e.q.ReleaseVariantStock(e.ctx, dbgen.ReleaseVariantStockParams{ID: mustUUID(t, variant), Qty: 2}))
	e.flushAll(d)
	require.Equal(t, 2, e.notifications(user, alerts.KindBackInStock))

	// Subscribing while sellable is rejected.
	code, _ = e.subscribe(e.tenA, user, map[string]any{"productId": prod, "variantId": variant, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusConflict, code)
}

func TestIntegrationProductLevelBackInStock(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Topi", 50000, false)

	code, _ := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusCreated, code)

	// Variant inserted with stock while the product flag is still off: not
	// sellable yet, so nothing fires and the subscription stays pending.
	e.insertVariant(prod, 50000, 4)
	d := e.dispatcher(0, 0)
	e.flushAll(d)
	require.Equal(t, 0, e.notifications(user, alerts.KindBackInStock))

	e.exec(`UPDATE products SET in_stock=true WHERE id=$1`, prod)
	e.flushAll(d)
	require.Equal(t, 1, e.notifications(user, alerts.KindBackInStock))
}

func TestIntegrationPriceDrop(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Sepatu Lari", 1250000, true)

	code, sub := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "PRICE_DROP"})
	require.Equal(t, http.StatusCreated, code)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE id=$1 AND price_at_subscribe=1250000`, sub))

	d := e.dispatcher(0, 0)
	e.exec(`UPDATE products SET price=1300000 WHERE id=$1`, prod)
	e.flushAll(d)
	require.Equal(t, 0, e.notifications(user, alerts.KindPriceDrop))

	// 1300000 -> 1260000 is a decrease but still above the subscribed price.
	e.exec(`UPDATE products SET price=1260000 WHERE id=$1`, prod)
	e.flushAll(d)
	require.Equal(t, 0, e.notifications(user, alerts.KindPriceDrop))

	e.exec(`UPDATE products SET price=999000 WHERE id=$1`, prod)
	e.flushAll(d)
	require.Equal(t, 1, e.notifications(user, alerts.KindPriceDrop))

	var body string
	var data []byte
	require.NoError(t, e.pool.QueryRow(e.ctx, `SELECT body, data FROM notifications WHERE user_id=$1 AND type='price_drop'`, user).Scan(&body, &data))
	require.Equal(t, "Sepatu Lari turun dari Rp 1.250.000 menjadi Rp 999.000.", body)
	var dm map[string]any
	require.NoError(t, json.Unmarshal(data, &dm))
	require.EqualValues(t, 1250000, dm["previousPrice"])
	require.EqualValues(t, 999000, dm["price"])
	require.Equal(t, sub, dm["subscriptionId"])
}

func TestIntegrationPreferenceOptOut(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	e.exec(`INSERT INTO push_preferences(user_id, enabled, order_updates, promo_updates, stock_updates) VALUES($1,true,true,true,false)`, user)
	prod := e.insertProduct(e.tenA, "Jaket", 300000, false)
	_, sub := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "BACK_IN_STOCK"})

	e.exec(`UPDATE products SET in_stock=true WHERE id=$1`, prod)
	res := e.flushAll(e.dispatcher(0, 0))
	require.Equal(t, 1, res.Claimed)
	require.Equal(t, 0, res.Notified)
	require.Equal(t, 0, e.notifications(user, alerts.KindBackInStock))
	require.Equal(t, 0, e.domainEvents(sub))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE id=$1 AND notified_at IS NOT NULL`, sub))
}

func TestIntegrationTenantIsolation(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prodA := e.insertProduct(e.tenA, "Kemeja", 200000, false)
	prodB := e.insertProduct(e.tenB, "Kemeja", 200000, false)

	code, subA := e.subscribe(e.tenA, user, map[string]any{"productId": prodA, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusCreated, code)

	// Product of tenant A is not visible through tenant B.
	code, _ = e.subscribe(e.tenB, user, map[string]any{"productId": prodA, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusNotFound, code)

	// Restocking B's same-titled product does not notify A's subscription.
	e.exec(`UPDATE products SET in_stock=true WHERE id=$1`, prodB)
	e.flushAll(e.dispatcher(0, 0))
	require.Equal(t, 0, e.notifications(user, alerts.KindBackInStock))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE id=$1 AND notified_at IS NULL`, subA))

	rec := e.do(http.MethodGet, "/alerts", e.tenB, user, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Data       []map[string]any `json:"data"`
		Pagination struct {
			TotalItems int `json:"totalItems"`
		} `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Empty(t, list.Data)

	rec = e.do(http.MethodGet, "/alerts?productId="+prodA, e.tenA, user, nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Data, 1)
	require.Equal(t, subA, list.Data[0]["id"])
	require.Equal(t, "active", list.Data[0]["status"])
	require.Equal(t, 1, list.Pagination.TotalItems)

	require.Equal(t, http.StatusNotFound, e.do(http.MethodDelete, "/alerts/"+subA, e.tenB, user, nil).Code)
	other, _ := e.insertUser()
	require.Equal(t, http.StatusNotFound, e.do(http.MethodDelete, "/alerts/"+subA, e.tenA, other, nil).Code)
	require.Equal(t, http.StatusNoContent, e.do(http.MethodDelete, "/alerts/"+subA, e.tenA, user, nil).Code)
	require.Equal(t, 0, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE id=$1`, subA))
}

func TestIntegrationConcurrentFlushFiresOnce(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Tas", 1250000, true)
	_, sub := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "PRICE_DROP"})

	for round := 1; round <= 10; round++ {
		if round > 1 {
			e.exec(`UPDATE products SET price=1250000 WHERE id=$1`, prod) // increase: no event
			code, _ := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "PRICE_DROP"})
			require.Equal(t, http.StatusOK, code)
		}
		e.exec(`UPDATE products SET price=1100000 WHERE id=$1`, prod)
		e.exec(`UPDATE products SET price=999000 WHERE id=$1`, prod)
		require.Equal(t, 2, e.count(`SELECT count(*) FROM product_alert_events WHERE product_id=$1 AND processed_at IS NULL`, prod))

		d1, d2 := e.dispatcher(0, 1), e.dispatcher(0, 1)
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, d := range []*alerts.Dispatcher{d1, d2} {
			wg.Add(1)
			go func(i int, d *alerts.Dispatcher) {
				defer wg.Done()
				<-start
				_, errs[i] = d.Flush(e.ctx)
			}(i, d)
		}
		close(start)
		wg.Wait()
		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		e.flushAll(d1) // in case one flusher saw no event at all

		require.Equal(t, round, e.notifications(user, alerts.KindPriceDrop), "round %d", round)
		require.Equal(t, round, e.domainEvents(sub), "round %d", round)
		require.Equal(t, 0, e.count(`SELECT count(*) FROM product_alert_events WHERE product_id=$1 AND processed_at IS NULL`, prod))
	}
}

func TestIntegrationBudgetLoop(t *testing.T) {
	e := newEnv(t)
	prod := e.insertProduct(e.tenA, "Kaos Putih", 100000, true)
	variant := e.insertVariant(prod, 100000, 0)
	users := make([]string, 5)
	for i := range users {
		users[i], _ = e.insertUser()
		code, _ := e.subscribe(e.tenA, users[i], map[string]any{"productId": prod, "variantId": variant, "type": "BACK_IN_STOCK"})
		require.Equal(t, http.StatusCreated, code)
	}
	e.exec(`UPDATE product_variants SET stock=10 WHERE id=$1`, variant)

	d := e.dispatcher(2, 0)
	res, err := d.Flush(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Claimed)
	require.True(t, res.More)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1 AND processed_at IS NULL`, variant))

	e.flushAll(d)
	total := 0
	for _, u := range users {
		n := e.notifications(u, alerts.KindBackInStock)
		require.Equal(t, 1, n)
		total += n
	}
	require.Equal(t, 5, total)
	require.Equal(t, 5, e.count(`SELECT count(*) FROM domain_events d JOIN product_alert_subscriptions s ON s.id=d.aggregate_id WHERE s.product_id=$1`, prod))
	require.Equal(t, 0, e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1 AND processed_at IS NULL`, variant))
}

// Non-actionable subscriptions beyond the budget, created before an
// actionable one, must neither consume the budget nor stall the event.
func TestIntegrationNonActionableDoNotStall(t *testing.T) {
	e := newEnv(t)
	const budget = 3

	t.Run("back in stock", func(t *testing.T) {
		prod := e.insertProduct(e.tenA, "Kaos Ukuran", 100000, true)
		sizeS := e.insertVariant(prod, 100000, 0)
		sizeM := e.insertVariant(prod, 100000, 0)
		for i := 0; i < budget+1; i++ {
			u, _ := e.insertUser()
			code, _ := e.subscribe(e.tenA, u, map[string]any{"productId": prod, "variantId": sizeS, "type": "BACK_IN_STOCK"})
			require.Equal(t, http.StatusCreated, code)
		}
		time.Sleep(5 * time.Millisecond) // strictly later created_at
		target, _ := e.insertUser()
		code, sub := e.subscribe(e.tenA, target, map[string]any{"productId": prod, "variantId": sizeM, "type": "BACK_IN_STOCK"})
		require.Equal(t, http.StatusCreated, code)

		e.exec(`UPDATE product_variants SET stock=4 WHERE id=$1`, sizeM)
		res, err := e.dispatcher(budget, 0).Flush(e.ctx)
		require.NoError(t, err)
		require.False(t, res.More, "drainer must not report more work")
		require.Equal(t, 1, res.Events)
		require.Equal(t, 1, res.Claimed)
		require.Equal(t, 1, e.notifications(target, alerts.KindBackInStock))
		require.Equal(t, 1, e.domainEvents(sub))
		require.Equal(t, budget+1, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE variant_id=$1 AND notified_at IS NULL`, sizeS))
	})

	t.Run("price drop", func(t *testing.T) {
		prod := e.insertProduct(e.tenA, "Celana", 1000, true)
		// Subscribed during a sale at 1000; the price then went back up.
		for i := 0; i < budget+1; i++ {
			u, _ := e.insertUser()
			e.exec(`INSERT INTO product_alert_subscriptions(tenant_id,user_id,product_id,alert_type,price_at_subscribe) VALUES($1,$2,$3,'PRICE_DROP',1000)`, e.tenA, u, prod)
		}
		e.exec(`UPDATE products SET price=1500 WHERE id=$1`, prod)
		time.Sleep(5 * time.Millisecond)
		target, _ := e.insertUser()
		code, sub := e.subscribe(e.tenA, target, map[string]any{"productId": prod, "type": "PRICE_DROP"})
		require.Equal(t, http.StatusCreated, code)

		e.exec(`UPDATE products SET price=1100 WHERE id=$1`, prod)
		res, err := e.dispatcher(budget, 0).Flush(e.ctx)
		require.NoError(t, err)
		require.False(t, res.More)
		require.Equal(t, 1, res.Claimed)
		require.Equal(t, 1, e.notifications(target, alerts.KindPriceDrop))
		require.Equal(t, 1, e.domainEvents(sub))
	})
}

func TestIntegrationNoWebhookDeliveries(t *testing.T) {
	e := newEnv(t)
	for _, ten := range []string{e.tenA, e.tenB} {
		e.exec(`INSERT INTO webhook_endpoints(name,url,secret,active,topics,tenant_id) VALUES('catch-all','https://example.invalid/hook','s',true,'{}',$1)`, ten)
	}
	before := e.count(`SELECT count(*) FROM webhook_deliveries`)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Sandal", 80000, false)
	_, sub := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "BACK_IN_STOCK"})
	e.exec(`UPDATE products SET in_stock=true WHERE id=$1`, prod)
	e.flushAll(e.dispatcher(0, 0))
	require.Equal(t, 1, e.domainEvents(sub))
	require.Equal(t, before, e.count(`SELECT count(*) FROM webhook_deliveries`))
}

func TestIntegrationRunnerDrains(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Dompet", 150000, false)
	_, _ = e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "BACK_IN_STOCK"})

	mailRunner := &alerts.Runner{Flusher: e.mailer(), Interval: time.Hour, Timeout: 5 * time.Second,
		OnError: func(err error) { t.Errorf("mail runner: %v", err) }}
	d := e.dispatcher(0, 0)
	d.Mailer = mailRunner // kicked after commit; its ticker never fires here
	r := &alerts.Runner{Flusher: d, Interval: 50 * time.Millisecond, Timeout: 5 * time.Second,
		OnError: func(err error) { t.Errorf("runner: %v", err) }}
	r.Start(context.Background())
	mailRunner.Start(context.Background())
	e.exec(`UPDATE products SET in_stock=true WHERE id=$1`, prod)

	deadline := time.Now().Add(2 * time.Second)
	for e.notifications(user, alerts.KindBackInStock) == 0 || e.capture.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runners did not notify and email within 2s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Stop(stopCtx))
	require.NoError(t, mailRunner.Stop(stopCtx))
	require.Equal(t, 1, e.capture.count())
}

// One popular product must not starve other events, in any tenant: the
// budget is shared across the events of a pass and an overflowing event
// rotates behind events that were not attempted yet.
func TestIntegrationTwoTenantFairness(t *testing.T) {
	e := newEnv(t)

	t.Run("same pass", func(t *testing.T) {
		prodA := e.insertProduct(e.tenA, "Populer A", 100000, true)
		varA := e.insertVariant(prodA, 100000, 0)
		for i := 0; i < 5; i++ {
			u, _ := e.insertUser()
			code, _ := e.subscribe(e.tenA, u, map[string]any{"productId": prodA, "variantId": varA, "type": "BACK_IN_STOCK"})
			require.Equal(t, http.StatusCreated, code)
		}
		prodB := e.insertProduct(e.tenB, "Biasa B", 50000, true)
		varB := e.insertVariant(prodB, 50000, 0)
		userB, _ := e.insertUser()
		code, _ := e.subscribe(e.tenB, userB, map[string]any{"productId": prodB, "variantId": varB, "type": "BACK_IN_STOCK"})
		require.Equal(t, http.StatusCreated, code)

		e.exec(`UPDATE product_variants SET stock=10 WHERE id=$1`, varA) // A's event first (lower id)
		e.exec(`UPDATE product_variants SET stock=10 WHERE id=$1`, varB)

		res, err := e.dispatcher(2, 0).Flush(e.ctx)
		require.NoError(t, err)
		require.Equal(t, 2, res.Claimed)
		require.True(t, res.More)
		require.Equal(t, 1, e.notifications(userB, alerts.KindBackInStock), "tenant B must be served in the first pass")
		require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1 AND processed_at IS NOT NULL`, varB))
		require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_events WHERE variant_id=$1 AND processed_at IS NULL AND last_attempt_at IS NOT NULL`, varA))

		e.flushAll(e.dispatcher(2, 0))
		require.Equal(t, 5, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE variant_id=$1 AND notified_at IS NOT NULL`, varA))
	})

	t.Run("later event jumps the queue", func(t *testing.T) {
		prodA := e.insertProduct(e.tenA, "Populer A2", 100000, true)
		varA := e.insertVariant(prodA, 100000, 0)
		for i := 0; i < 5; i++ {
			u, _ := e.insertUser()
			code, _ := e.subscribe(e.tenA, u, map[string]any{"productId": prodA, "variantId": varA, "type": "BACK_IN_STOCK"})
			require.Equal(t, http.StatusCreated, code)
		}
		e.exec(`UPDATE product_variants SET stock=10 WHERE id=$1`, varA)
		d := e.dispatcher(2, 0)
		res, err := d.Flush(e.ctx)
		require.NoError(t, err)
		require.Equal(t, 2, res.Claimed)
		require.True(t, res.More)

		// B restocks after A's event was already attempted once.
		prodB := e.insertProduct(e.tenB, "Biasa B2", 50000, true)
		varB := e.insertVariant(prodB, 50000, 0)
		userB, _ := e.insertUser()
		code, _ := e.subscribe(e.tenB, userB, map[string]any{"productId": prodB, "variantId": varB, "type": "BACK_IN_STOCK"})
		require.Equal(t, http.StatusCreated, code)
		e.exec(`UPDATE product_variants SET stock=1 WHERE id=$1`, varB)

		// Only one claim per pass: B (never attempted) must win it.
		res, err = e.dispatcher(1, 0).Flush(e.ctx)
		require.NoError(t, err)
		require.Equal(t, 1, res.Claimed)
		require.Equal(t, 1, e.notifications(userB, alerts.KindBackInStock))
		e.flushAll(d)
		require.Equal(t, 5, e.count(`SELECT count(*) FROM product_alert_subscriptions WHERE variant_id=$1 AND notified_at IS NOT NULL`, varA))
	})
}

// Sellability matches the cart exactly: a variant with stock is sellable even
// when products.in_stock is false (e.g. after a platform inventory update set
// every variant to 0 and a pending order expiry released stock to one).
func TestIntegrationVariantRestockIgnoresProductFlag(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Kaos Flag", 100000, false)
	variant := e.insertVariant(prod, 100000, 0)
	code, _ := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "variantId": variant, "type": "BACK_IN_STOCK"})
	require.Equal(t, http.StatusCreated, code)

	require.NoError(t, e.q.ReleaseVariantStock(e.ctx, dbgen.ReleaseVariantStockParams{ID: mustUUID(t, variant), Qty: 3}))
	e.flushAll(e.dispatcher(0, 0))
	require.Equal(t, 1, e.notifications(user, alerts.KindBackInStock))
}

// Emails survive relay failures: the delivery row is retried with backoff
// and nothing is lost when the first attempts fail.
func TestIntegrationMailerRetriesFailedDelivery(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Jam", 400000, false)
	_, sub := e.subscribe(e.tenA, user, map[string]any{"productId": prod, "type": "BACK_IN_STOCK"})
	e.exec(`UPDATE products SET in_stock=true WHERE id=$1`, prod)
	e.flushAll(e.dispatcher(0, 0))

	failing := &failingNotifier{}
	m := e.mailer(failing)
	res, err := m.Flush(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Attempted)
	require.Equal(t, 0, res.Delivered)
	q := `SELECT count(*) FROM product_alert_deliveries pd JOIN domain_events de ON de.id=pd.event_id
	      WHERE de.aggregate_id=$1 AND pd.delivered_at IS NULL AND pd.failed_at IS NULL AND pd.attempts=1
	        AND pd.last_error LIKE '%relay down%' AND pd.next_attempt_at > now()`
	require.Equal(t, 1, e.count(q, sub))

	// Not due yet: nothing leased.
	res, err = m.Flush(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 0, res.Attempted)

	// Backoff elapsed and the relay is back.
	e.exec(`UPDATE product_alert_deliveries SET next_attempt_at = now() - interval '1 second' WHERE tenant_id=$1`, e.tenA)
	require.Equal(t, 1, e.drainMail(e.mailer()))
	require.Equal(t, 1, e.capture.count())
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_deliveries pd JOIN domain_events de ON de.id=pd.event_id WHERE de.aggregate_id=$1 AND pd.delivered_at IS NOT NULL AND pd.attempts=2`, sub))
}

func TestIntegrationPruneProcessedEvents(t *testing.T) {
	e := newEnv(t)
	prod := e.insertProduct(e.tenA, "Lama", 1000, true)
	e.exec(`UPDATE products SET price=900 WHERE id=$1`, prod)
	e.exec(`UPDATE products SET price=800 WHERE id=$1`, prod)
	e.flushAll(e.dispatcher(0, 0))
	require.Equal(t, 2, e.count(`SELECT count(*) FROM product_alert_events WHERE product_id=$1 AND processed_at IS NOT NULL`, prod))
	e.exec(`UPDATE product_alert_events SET processed_at = now() - interval '8 days' WHERE product_id=$1 AND id = (SELECT min(id) FROM product_alert_events WHERE product_id=$1)`, prod)

	d := e.dispatcher(0, 0)
	d.Retention = 7 * 24 * time.Hour
	_, err := d.Flush(e.ctx)
	require.NoError(t, err)
	require.Equal(t, 1, e.count(`SELECT count(*) FROM product_alert_events WHERE product_id=$1`, prod), "only the old processed event is pruned")
}

// hookStore runs afterCheck once, right after Subscribe's eligibility read and
// before the subscription is written.
type hookStore struct {
	*dbgen.Queries
	afterCheck func()
}

func (h *hookStore) GetAlertTarget(ctx context.Context, arg dbgen.GetAlertTargetParams) (dbgen.GetAlertTargetRow, error) {
	row, err := h.Queries.GetAlertTarget(ctx, arg)
	if h.afterCheck != nil {
		f := h.afterCheck
		h.afterCheck = nil
		f()
	}
	return row, err
}

// A restock that commits and is fully drained between Subscribe's check and
// the subscription's commit must still notify the subscriber.
func TestIntegrationSubscribeRestockRace(t *testing.T) {
	e := newEnv(t)
	user, _ := e.insertUser()
	prod := e.insertProduct(e.tenA, "Balapan", 100000, true)
	variant := e.insertVariant(prod, 100000, 0)
	d := e.dispatcher(0, 0)

	store := &hookStore{Queries: e.q, afterCheck: func() {
		e.exec(`UPDATE product_variants SET stock=2 WHERE id=$1`, variant)
		e.flushAll(d) // drains the restock event before the subscription exists
	}}
	svc := &alerts.Service{Q: store}
	_, created, err := svc.Subscribe(e.ctx, mustUUID(t, user), mustUUID(t, e.tenA), alerts.SubscribeInput{
		ProductID: mustUUID(t, prod), VariantID: mustUUID(t, variant), Type: alerts.TypeBackInStock,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 0, e.notifications(user, alerts.KindBackInStock))

	e.flushAll(d)
	require.Equal(t, 1, e.notifications(user, alerts.KindBackInStock))
}

type failingNotifier struct{}

func (failingNotifier) Notify(context.Context, dbgen.DomainEvent) error {
	return errors.New("relay down")
}

func mustUUID(t *testing.T, s string) (u pgtype.UUID) {
	t.Helper()
	require.NoError(t, u.Scan(s))
	return u
}
