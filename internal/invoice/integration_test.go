package invoice_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/invoice"
	"github.com/noah-isme/backend-toko/internal/money"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Postgres-backed invoice tests. Opt-in via TEST_DATABASE_URL.

type ienv struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	router http.Handler
	tenA   string
	tenB   string
	users  []string
}

func rnd(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func newIEnv(t *testing.T) *ienv {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	require.NoError(t, err)
	cfg.MaxConns = 25
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	e := &ienv{t: t, ctx: ctx, pool: pool}
	e.tenA = e.insertTenant()
	e.tenB = e.insertTenant()
	t.Cleanup(func() {
		c := context.Background()
		tenants := []string{e.tenA, e.tenB}
		for _, stmt := range []string{
			`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`,
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

	svc := &invoice.Service{Q: dbgen.New(pool), Pool: pool, Renderer: &invoice.Renderer{Compress: false}}
	h := &invoice.Handler{Svc: svc}
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
	r.Get("/orders/{orderId}/invoice", h.UserInvoice)
	r.Get("/admin/orders/{id}/invoice", h.AdminInvoice)
	e.router = r
	return e
}

func (e *ienv) insertTenant() string {
	var id string
	require.NoError(e.t, e.pool.QueryRow(e.ctx, `INSERT INTO tenants(slug,name,contact_email,timezone) VALUES($1,'Toko Integrasi','toko@example.com','Asia/Jakarta') RETURNING id::text`, rnd("it-invoice")).Scan(&id))
	return id
}

func (e *ienv) insertUser(name string) string {
	var id string
	require.NoError(e.t, e.pool.QueryRow(e.ctx, `INSERT INTO users(name,email,password_hash) VALUES($1,$2,'x') RETURNING id::text`, name, rnd("inv-buyer")+"@example.com").Scan(&id))
	e.users = append(e.users, id)
	return id
}

// insertOrder seeds an order with one item (Kaos Hitam 249000 x 5), shipping
// 5000, total 1250000 and a payment row.
func (e *ienv) insertOrder(tenantID, userID, status, paymentStatus string) string {
	var productID, orderID string
	require.NoError(e.t, e.pool.QueryRow(e.ctx, `INSERT INTO products(title,slug,price,tenant_id) VALUES('Kaos Hitam',$1,249000,$2) RETURNING id::text`, rnd("inv-prod"), tenantID).Scan(&productID))
	require.NoError(e.t, e.pool.QueryRow(e.ctx, `
		INSERT INTO orders(user_id,status,currency,pricing_subtotal,pricing_discount,pricing_tax,pricing_shipping,pricing_total,shipping_address,tenant_id,order_number)
		VALUES($1,$2::order_status,'IDR',1245000,0,0,5000,1250000,
		       '{"receiverName":"Budi Santoso","phone":"08123","addressLine1":"Jl. Merdeka No. 1","city":"Bandung","province":"Jawa Barat","postalCode":"40111","country":"ID"}',
		       $3,$4)
		RETURNING id::text`, userID, status, tenantID, rnd("ORD")).Scan(&orderID))
	_, err := e.pool.Exec(e.ctx, `INSERT INTO order_items(order_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'Kaos Hitam','kaos-hitam',5,249000,1245000)`, orderID, productID)
	require.NoError(e.t, err)
	if paymentStatus != "" {
		_, err = e.pool.Exec(e.ctx, `INSERT INTO payments(order_id,provider,status,channel,amount,tenant_id) VALUES($1,'mock',$2::payment_status,'bank_transfer',1250000,$3)`, orderID, paymentStatus, tenantID)
		require.NoError(e.t, err)
	}
	return orderID
}

func (e *ienv) get(path, tenantID, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Tenant-ID", tenantID)
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func filename(rec *httptest.ResponseRecorder) string {
	cd := rec.Header().Get("Content-Disposition")
	return strings.TrimSuffix(strings.TrimPrefix(cd, `attachment; filename="`), `"`)
}

func TestIntegrationInvoice(t *testing.T) {
	e := newIEnv(t)
	owner := e.insertUser("Budi Santoso")
	stranger := e.insertUser("Orang Lain")
	order := e.insertOrder(e.tenA, owner, "PAID", "PAID")
	year := time.Now().In(mustLoc(t, "Asia/Jakarta")).Year()
	first := fmt.Sprintf("INV-%d-000001", year)

	rec := e.get("/orders/"+order+"/invoice", e.tenA, owner)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/pdf", rec.Header().Get("Content-Type"))
	require.Equal(t, first+".pdf", filename(rec))
	require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))

	var total, unit int64
	var currency string
	require.NoError(t, e.pool.QueryRow(e.ctx, `SELECT o.pricing_total, o.currency, i.unit_price FROM orders o JOIN order_items i ON i.order_id=o.id WHERE o.id=$1`, order).Scan(&total, &currency, &unit))
	pdf := rec.Body.String()
	require.True(t, strings.HasPrefix(pdf, "%PDF-1."))
	for _, want := range []string{first, "Kaos Hitam", "Budi Santoso", "Toko Integrasi", "Lunas", "bank_transfer",
		money.Format(currency, total), money.Format(currency, unit), "Rp 1.250.000", "Rp 249.000"} {
		require.Contains(t, pdf, want)
	}
	require.Equal(t, 1, e.count(`SELECT count(*) FROM order_invoices WHERE order_id=$1 AND invoice_number=$2`, order, first))

	// Stable number on repeat and through the admin endpoint.
	require.Equal(t, first+".pdf", filename(e.get("/orders/"+order+"/invoice", e.tenA, owner)))
	adminRec := e.get("/admin/orders/"+order+"/invoice", e.tenA, "")
	require.Equal(t, http.StatusOK, adminRec.Code)
	require.Equal(t, first+".pdf", filename(adminRec))

	// Ownership and tenant isolation.
	require.Equal(t, http.StatusNotFound, e.get("/orders/"+order+"/invoice", e.tenA, stranger).Code)
	require.Equal(t, http.StatusNotFound, e.get("/orders/"+order+"/invoice", e.tenB, owner).Code)
	require.Equal(t, http.StatusNotFound, e.get("/admin/orders/"+order+"/invoice", e.tenB, "").Code)

	// Not yet paid -> 409; nothing issued.
	pending := e.insertOrder(e.tenA, owner, "PENDING_PAYMENT", "PENDING")
	rec = e.get("/orders/"+pending+"/invoice", e.tenA, owner)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "INVOICE_NOT_AVAILABLE")
	require.Equal(t, 0, e.count(`SELECT count(*) FROM order_invoices WHERE order_id=$1`, pending))
	cancelled := e.insertOrder(e.tenA, owner, "CANCELLED", "EXPIRED")
	require.Equal(t, http.StatusConflict, e.get("/orders/"+cancelled+"/invoice", e.tenA, owner).Code)

	// Next order in the same tenant gets 000002; tenant B starts at 000001.
	second := e.insertOrder(e.tenA, owner, "SHIPPED", "PAID")
	require.Equal(t, fmt.Sprintf("INV-%d-000002.pdf", year), filename(e.get("/admin/orders/"+second+"/invoice", e.tenA, "")))
	ownerB := e.insertUser("Pembeli B")
	orderB := e.insertOrder(e.tenB, ownerB, "DELIVERED", "PAID")
	require.Equal(t, first+".pdf", filename(e.get("/orders/"+orderB+"/invoice", e.tenB, ownerB)))

	// An issued invoice stays downloadable after a later refund/cancel.
	e.exec(`UPDATE orders SET status='CANCELLED' WHERE id=$1`, second)
	e.exec(`UPDATE payments SET status='REFUNDED' WHERE order_id=$1`, second)
	rec = e.get("/admin/orders/"+second+"/invoice", e.tenA, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Dikembalikan")

	// orders table untouched by this feature.
	require.Equal(t, 0, e.count(`SELECT count(*) FROM information_schema.columns WHERE table_name='orders' AND column_name LIKE 'invoice%'`))
}

func TestIntegrationInvoiceConcurrentFirstRequests(t *testing.T) {
	e := newIEnv(t)
	owner := e.insertUser("Budi")
	order := e.insertOrder(e.tenA, owner, "PAID", "PAID")

	const n = 20
	names := make([]string, n)
	codes := make([]int, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := e.get("/orders/"+order+"/invoice", e.tenA, owner)
			codes[i], names[i] = rec.Code, filename(rec)
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		require.Equal(t, http.StatusOK, codes[i])
		require.Equal(t, names[0], names[i])
	}
	require.True(t, strings.HasSuffix(names[0], "-000001.pdf"), names[0])
	require.Equal(t, 1, e.count(`SELECT last_number::int FROM invoice_counters WHERE tenant_id=$1`, e.tenA))
	require.Equal(t, 1, e.count(`SELECT count(*) FROM order_invoices WHERE order_id=$1`, order))
}

func (e *ienv) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(e.ctx, sql, args...).Scan(&n))
	return n
}

func (e *ienv) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(e.ctx, sql, args...)
	require.NoError(e.t, err)
}

func mustLoc(t *testing.T, name string) *time.Location {
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return loc
}
