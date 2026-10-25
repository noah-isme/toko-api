package admin_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/admin"
	"github.com/noah-isme/backend-toko/internal/campaign"
	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/checkout"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/platform"
	"github.com/noah-isme/backend-toko/internal/security"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Postgres integration tests for the catalog-ops stream (bulk updates, CSV
// export/import, low stock, product status enforcement, tenant-scoped product
// webhooks). Opt-in: set TEST_DATABASE_URL to a migrated database.

// productionBodyLimit is the SECURITY_BODY_LIMIT_BYTES default in cmd/api.
const productionBodyLimit = 1_048_576

type opsEnv struct {
	t       *testing.T
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	router  http.Handler
	catalog *admin.CatalogHandler
	orders  *admin.OrdersHandler
	bus     *events.Bus
	tenA    string
	tenB    string
	user    string
	start   time.Time
	tag     string
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newOpsEnv(t *testing.T) *opsEnv {
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

	e := &opsEnv{t: t, pool: pool, q: dbgen.New(pool), start: time.Now().Add(-time.Second), tag: strings.ToUpper(randHex(3))}
	e.tenA = e.insertTenant()
	e.tenB = e.insertTenant()
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users(name,email,password_hash) VALUES('IT Admin',$1,'x') RETURNING id::text`, "it-"+randHex(5)+"@example.test").Scan(&e.user))
	t.Cleanup(e.cleanup)

	e.bus = &events.Bus{Store: e.q, Scheduler: &notify.Dispatcher{Store: notify.QueriesStore{Queries: e.q}, Enabled: true}}
	e.catalog = &admin.CatalogHandler{Q: e.q, Pool: pool, Events: e.bus, AuditEnabled: true}
	e.orders = &admin.OrdersHandler{Q: e.q}
	plat := &platform.Handler{Pool: pool, Events: e.bus}
	analytics := &admin.AnalyticsHandler{Q: e.q}
	flash := &campaign.Handler{Pool: pool}

	// The router mirrors cmd/api: the global BodyLimit (production default
	// 1 MiB, with the admin overrides) runs before routing, and the routes are
	// mounted under /api/v1 so the override paths match. e.do adds the prefix.
	root := chi.NewRouter()
	root.Use(middleware.RequestID)
	root.Use(security.BodyLimit{Max: productionBodyLimit, Overrides: admin.BodyLimitOverrides()}.Middleware)
	r := chi.NewRouter()
	root.Mount("/api/v1", r)
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
	r.Get("/admin/products", e.catalog.ListProducts)
	r.Post("/admin/products/bulk/price", e.catalog.BulkPrice)
	r.Post("/admin/products/bulk/stock", e.catalog.BulkStock)
	r.Post("/admin/products/bulk/status", e.catalog.BulkStatus)
	r.Get("/admin/products/export", func(w http.ResponseWriter, req *http.Request) { e.catalog.ExportProductsCSV(w, req) })
	r.Post("/admin/products/import", e.catalog.ImportProductsCSV)
	r.Get("/admin/orders/export", func(w http.ResponseWriter, req *http.Request) { e.orders.ExportOrdersCSV(w, req) })
	r.Get("/admin/inventory", plat.InventoryList)
	r.Patch("/admin/settings", plat.SettingsUpdate)
	r.Get("/admin/analytics/overview", analytics.Overview)
	r.Get("/flash-sales", flash.Public)
	e.router = root
	return e
}

func (e *opsEnv) cleanup() {
	ctx := context.Background()
	tenants := []string{e.tenA, e.tenB}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM webhook_deliveries WHERE endpoint_id IN (SELECT id FROM webhook_endpoints WHERE tenant_id = ANY($1::uuid[]))
		   OR event_id IN (SELECT id FROM domain_events WHERE tenant_id = ANY($1::uuid[]) OR payload->>'itTag' = $2)`, []any{tenants, e.tag}},
		{`DELETE FROM domain_events WHERE tenant_id = ANY($1::uuid[]) OR payload->>'itTag' = $2`, []any{tenants, e.tag}},
		{`DELETE FROM webhook_endpoints WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM audit_logs WHERE actor_user_id = $1::uuid`, []any{e.user}},
		{`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM carts WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM vouchers WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM user_product_views WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM flash_sale_campaigns WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM categories WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM tenant_settings WHERE tenant_id = ANY($1::uuid[])`, []any{tenants}},
		{`DELETE FROM addresses WHERE user_id = $1::uuid`, []any{e.user}},
		{`DELETE FROM users WHERE id = $1::uuid`, []any{e.user}},
		{`DELETE FROM tenants WHERE id = ANY($1::uuid[])`, []any{tenants}},
	}
	for _, s := range stmts {
		if _, err := e.pool.Exec(ctx, s.sql, s.args...); err != nil {
			e.t.Errorf("cleanup %q: %v", s.sql, err)
		}
	}
}

func (e *opsEnv) insertTenant() string {
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `INSERT INTO tenants(slug,name) VALUES($1,'IT ops') RETURNING id::text`, "it-ops-"+randHex(5)).Scan(&id))
	return id
}

type seededProduct struct {
	ID   string
	Slug string
}

func (e *opsEnv) product(tenantID, title string, price int64) seededProduct {
	e.t.Helper()
	slug := "it-" + strings.ToLower(e.tag) + "-" + randHex(4)
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`INSERT INTO products(title,slug,price,tenant_id,in_stock) VALUES($1,$2,$3,$4,true) RETURNING id::text`, title, slug, price, tenantID).Scan(&id))
	return seededProduct{ID: id, Slug: slug}
}

func (e *opsEnv) variant(productID, sku string, price int64, stock int32) string {
	e.t.Helper()
	var skuArg any
	if sku != "" {
		skuArg = sku
	}
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`INSERT INTO product_variants(product_id,sku,price,stock,attributes) VALUES($1,$2,$3,$4,'{"size":"42"}') RETURNING id::text`, productID, skuArg, price, stock).Scan(&id))
	_, err := e.pool.Exec(context.Background(), `UPDATE products SET in_stock = EXISTS(SELECT 1 FROM product_variants WHERE product_id=products.id AND stock>0) WHERE id=$1`, productID)
	require.NoError(e.t, err)
	return id
}

func (e *opsEnv) sku(name string) string { return e.tag + "-" + name }

func (e *opsEnv) do(method, path, tenantID string, body []byte, contentType string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(body))
	req.Header.Set("X-Tenant-ID", tenantID)
	req.Header.Set("X-User-ID", e.user)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *opsEnv) postJSON(path, tenantID string, body any) *httptest.ResponseRecorder {
	data, err := json.Marshal(body)
	require.NoError(e.t, err)
	return e.do(http.MethodPost, path, tenantID, data, "application/json")
}

func (e *opsEnv) importCSV(tenantID, typ string, dryRun bool, data []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "import.csv")
	require.NoError(e.t, err)
	_, _ = fw.Write(data)
	require.NoError(e.t, mw.Close())
	return e.do(http.MethodPost, fmt.Sprintf("/admin/products/import?type=%s&dryRun=%t", typ, dryRun), tenantID, buf.Bytes(), mw.FormDataContentType())
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func (e *opsEnv) scalar(sql string, args ...any) int64 {
	e.t.Helper()
	var n int64
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

// catalogChecksum fingerprints every product and variant of a tenant.
func (e *opsEnv) catalogChecksum(tenantID string) string {
	e.t.Helper()
	var sum string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `
		SELECT md5(COALESCE(string_agg(row_text, '|' ORDER BY row_text), '')) FROM (
		  SELECT p.id::text||p.title||p.slug||p.status||p.price||COALESCE(p.compare_at::text,'')||p.in_stock||COALESCE(p.description,'')||COALESCE(p.low_stock_threshold::text,'')||p.updated_at::text AS row_text
		  FROM products p WHERE p.tenant_id=$1
		  UNION ALL
		  SELECT v.id::text||COALESCE(v.sku,'')||v.price||v.stock||v.attributes::text FROM product_variants v JOIN products p ON p.id=v.product_id WHERE p.tenant_id=$1
		) s`, tenantID).Scan(&sum))
	return sum
}

func (e *opsEnv) productEvents(tenantID string) int64 {
	return e.scalar(`SELECT COUNT(*) FROM domain_events WHERE tenant_id=$1 AND topic LIKE 'product.%'`, tenantID)
}

func (e *opsEnv) auditCount(action string) int64 {
	return e.scalar(`SELECT COUNT(*) FROM audit_logs WHERE action=$1 AND actor_user_id=$2::uuid`, action, e.user)
}

func readCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	require.True(t, bytes.HasPrefix(body, []byte("\xEF\xBB\xBF")), "missing BOM")
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF"))))
	rows, err := r.ReadAll()
	require.NoError(t, err)
	return rows
}

func colIndex(header []string) map[string]int {
	m := map[string]int{}
	for i, h := range header {
		m[h] = i
	}
	return m
}

type apiError struct {
	Error struct {
		Code    string          `json:"code"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

type importResp struct {
	Data struct {
		DryRun  bool `json:"dryRun"`
		Summary struct {
			Total, Create, CreateVariant, Update, Unchanged, Rejected int
		} `json:"summary"`
		Rows []struct {
			Row       int                       `json:"row"`
			SKU       string                    `json:"sku"`
			VariantID string                    `json:"variantId"`
			ProductID string                    `json:"productId"`
			Slug      string                    `json:"slug"`
			Action    string                    `json:"action"`
			Changes   map[string]map[string]any `json:"changes"`
			Errors    []struct{ Field, Code string }
		} `json:"rows"`
	} `json:"data"`
}

func importRespFrom422(t *testing.T, rec *httptest.ResponseRecorder) importResp {
	t.Helper()
	var wrapper struct {
		Error struct {
			Code    string          `json:"code"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &wrapper))
	require.Equal(t, "VALIDATION_FAILED", wrapper.Error.Code)
	var out importResp
	require.NoError(t, json.Unmarshal([]byte(`{"data":`+string(wrapper.Error.Details)+`}`), &out))
	return out
}

// ---------------------------------------------------------------------------

func TestIntegrationProductListSortAndFilters(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	a := e.product(e.tenA, "Alpha", 300)
	b := e.product(e.tenA, "Bravo", 100)
	c := e.product(e.tenA, "Charlie", 200)
	e.variant(a.ID, e.sku("A"), 300, 50)
	e.variant(b.ID, e.sku("B"), 100, 3)
	e.variant(c.ID, e.sku("C"), 200, 0)
	_, err := e.pool.Exec(ctx, `UPDATE products SET status='draft' WHERE id=$1`, c.ID)
	require.NoError(t, err)
	_, err = e.pool.Exec(ctx, `UPDATE products SET updated_at = now() + interval '1 hour' WHERE id=$1`, b.ID)
	require.NoError(t, err)
	e.product(e.tenB, "Other tenant", 1)

	type listResp struct {
		Data []struct {
			ID, Title, Status, StockStatus string
			LowStockThreshold              *int32
		} `json:"data"`
	}
	titles := func(query string) []string {
		rec := e.do(http.MethodGet, "/admin/products?"+query, e.tenA, nil, "")
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var out []string
		for _, p := range decode[listResp](t, rec).Data {
			out = append(out, p.Title)
		}
		return out
	}
	require.Equal(t, []string{"Alpha", "Bravo", "Charlie"}, titles("sort=name"))
	require.Equal(t, []string{"Charlie", "Bravo", "Alpha"}, titles("sort=name:desc"))
	require.Equal(t, []string{"Bravo", "Charlie", "Alpha"}, titles("sort=price:asc"))
	require.Equal(t, []string{"Alpha", "Charlie", "Bravo"}, titles("sort=price:desc"))
	require.Equal(t, []string{"Charlie", "Bravo", "Alpha"}, titles("sort=stock:asc"))
	require.Equal(t, []string{"Alpha", "Bravo", "Charlie"}, titles("sort=stock:desc"))
	require.Equal(t, []string{"Alpha", "Bravo", "Charlie"}, titles("sort=created:asc"))
	require.Equal(t, []string{"Charlie", "Bravo", "Alpha"}, titles("sort=created:desc"))
	require.Equal(t, []string{"Charlie", "Bravo", "Alpha"}, titles(""))
	require.Equal(t, "Bravo", titles("sort=updated:desc")[0])
	require.Equal(t, []string{"Charlie"}, titles("status=draft"))
	require.Equal(t, []string{"Bravo"}, titles("stockStatus=low"))
	require.Equal(t, []string{"Charlie"}, titles("stockStatus=out"))
	require.Equal(t, []string{"Alpha"}, titles("stockStatus=in"))
	require.Equal(t, []string{"Alpha", "Bravo"}, titles("inStock=true&sort=name"))
	require.Equal(t, 400, e.do(http.MethodGet, "/admin/products?sort=title", e.tenA, nil, "").Code)
	require.Equal(t, 400, e.do(http.MethodGet, "/admin/products?status=live", e.tenA, nil, "").Code)
}

func TestIntegrationBulkPrice(t *testing.T) {
	e := newOpsEnv(t)
	ctx := tenant.WithTenant(context.Background(), e.tenA)
	p := e.product(e.tenA, "Sepatu", 10000)
	v1 := e.variant(p.ID, e.sku("P1"), 10000, 10)
	v2 := e.variant(p.ID, e.sku("P2"), 11000, 10)
	q := e.product(e.tenA, "Kaos", 5000)
	qv := e.variant(q.ID, e.sku("Q1"), 5000, 10)
	other := e.product(e.tenB, "Other", 1)
	cartSvc := &cart.Service{Q: e.q, Pool: e.pool}

	// applyTo=both (default): product and every variant price.
	rec := e.postJSON("/admin/products/bulk/price", e.tenA, map[string]any{"items": []map[string]any{{"productId": p.ID, "price": 12000, "compareAt": 15000}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 12000, e.scalar(`SELECT price FROM products WHERE id=$1`, p.ID))
	require.EqualValues(t, 15000, e.scalar(`SELECT compare_at FROM products WHERE id=$1`, p.ID))
	require.EqualValues(t, 12000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, v1))
	require.EqualValues(t, 12000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, v2))
	// product price + compare_at + 2 variant prices
	require.EqualValues(t, 4, e.scalar(`SELECT COUNT(*) FROM domain_events WHERE tenant_id=$1 AND topic='product.price_changed'`, e.tenA))
	require.EqualValues(t, 1, e.auditCount("catalog.bulk.price"))
	var reqID pgtype.Text
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT request_id FROM audit_logs WHERE action='catalog.bulk.price' AND actor_user_id=$1::uuid`, e.user).Scan(&reqID))
	require.True(t, reqID.Valid && reqID.String != "", "audit row must carry chi's request id")
	var payload map[string]any
	var raw []byte
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT payload FROM domain_events WHERE tenant_id=$1 AND topic='product.price_changed' LIMIT 1`, e.tenA).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, reqID.String, payload["requestId"])
	require.Equal(t, e.user, payload["actorUserId"])

	// Cart charges the new variant price.
	userID := e.user
	c, err := cartSvc.EnsureCart(ctx, &userID, nil)
	require.NoError(t, err)
	cartID := cart.UUIDString(c.ID)
	require.NoError(t, cartSvc.AddItem(ctx, cartID, p.ID, &v1, 1))
	items, err := e.q.ListCartItems(ctx, c.ID)
	require.NoError(t, err)
	require.EqualValues(t, 12000, items[0].UnitPrice)

	// applyTo=product leaves variants (and so the cart price) untouched.
	rec = e.postJSON("/admin/products/bulk/price", e.tenA, map[string]any{"items": []map[string]any{{"productId": q.ID, "price": 4000, "applyTo": "product"}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 4000, e.scalar(`SELECT price FROM products WHERE id=$1`, q.ID))
	require.EqualValues(t, 5000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, qv))
	require.NoError(t, cartSvc.AddItem(ctx, cartID, q.ID, &qv, 1))
	items, err = e.q.ListCartItems(ctx, c.ID)
	require.NoError(t, err)
	for _, it := range items {
		if cart.UUIDString(it.ProductID) == q.ID {
			require.EqualValues(t, 5000, it.UnitPrice, "applyTo=product keeps charging the variant price")
		}
	}

	// Variant item.
	rec = e.postJSON("/admin/products/bulk/price", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v2, "price": 13000}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 13000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, v2))
	require.EqualValues(t, 12000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, v1))

	// Other tenant's product is NOT_FOUND and nothing changes.
	before := e.catalogChecksum(e.tenA)
	rec = e.postJSON("/admin/products/bulk/price", e.tenA, map[string]any{"items": []map[string]any{{"productId": p.ID, "price": 1}, {"productId": other.ID, "price": 2}}})
	require.Equal(t, 422, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"code":"NOT_FOUND"`)
	require.Equal(t, before, e.catalogChecksum(e.tenA))
	require.EqualValues(t, 1, e.scalar(`SELECT price FROM products WHERE id=$1`, other.ID))
}

func TestIntegrationBulkStock(t *testing.T) {
	e := newOpsEnv(t)
	ctx := context.Background()
	p := e.product(e.tenA, "Topi", 1000)
	v1 := e.variant(p.ID, e.sku("T1"), 1000, 10)
	v2 := e.variant(p.ID, e.sku("T2"), 1000, 0)

	rec := e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "stock": 0}, {"variantId": v2, "delta": 0}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 0, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v1))
	require.EqualValues(t, 0, e.scalar(`SELECT in_stock::int FROM products WHERE id=$1`, p.ID), "in_stock recomputed")

	rec = e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "stock": 10}, {"variantId": v2, "delta": 4}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 4, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v2))
	require.EqualValues(t, 1, e.scalar(`SELECT in_stock::int FROM products WHERE id=$1`, p.ID))

	// NEGATIVE_RESULT rolls back the whole request.
	rec = e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "stock": 99}, {"variantId": v2, "delta": -5}}})
	require.Equal(t, 422, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "NEGATIVE_RESULT")
	require.EqualValues(t, 10, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v1))

	// A delta past int4 is a 422 OUT_OF_RANGE, not a 500.
	rec = e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "delta": 2147483647}}})
	require.Equal(t, 422, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "OUT_OF_RANGE")
	require.EqualValues(t, 10, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v1))

	// Lock order: bulk stock locks the product before its variants, like
	// import and bulk price. A writer holding the product lock and then
	// taking the variant lock must not deadlock with it.
	ptx, err := e.pool.Begin(ctx)
	require.NoError(t, err)
	_, err = ptx.Exec(ctx, `SELECT 1 FROM products WHERE id=$1 FOR UPDATE`, p.ID)
	require.NoError(t, err)
	waiting := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		waiting <- e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "delta": 1}}})
	}()
	select {
	case <-waiting:
		t.Fatal("bulk stock must wait for the product lock")
	case <-time.After(400 * time.Millisecond):
	}
	lockCtx, cancelLock := context.WithTimeout(ctx, 3*time.Second)
	_, err = ptx.Exec(lockCtx, `SELECT 1 FROM product_variants WHERE id=$1 FOR UPDATE`, v1)
	cancelLock()
	require.NoError(t, err, "variant must still be free while bulk stock waits on the product")
	require.NoError(t, ptx.Commit(ctx))
	rec = <-waiting
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 11, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v1))
	rec = e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "stock": 10}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())

	// Concurrency: a checkout reservation holding the row lock makes the bulk
	// delta wait, then apply on top of the reserved stock (12, not 15).
	tx, err := e.pool.Begin(ctx)
	require.NoError(t, err)
	_, err = dbgen.New(tx).ReserveVariantStock(ctx, dbgen.ReserveVariantStockParams{ID: mustUUID(v1), Qty: 3})
	require.NoError(t, err)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v1, "delta": 5}}})
	}()
	select {
	case <-done:
		t.Fatal("bulk stock must wait for the reservation's row lock")
	case <-time.After(400 * time.Millisecond):
	}
	require.NoError(t, tx.Commit(ctx))
	rec = <-done
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"stock":12`)
	require.EqualValues(t, 12, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v1))

	// A lock held past lock_timeout (5s) yields 409 and changes nothing.
	tx, err = e.pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT 1 FROM product_variants WHERE id=$1 FOR UPDATE`, v2)
	require.NoError(t, err)
	rec = e.postJSON("/admin/products/bulk/stock", e.tenA, map[string]any{"items": []map[string]any{{"variantId": v2, "stock": 1}}})
	require.NoError(t, tx.Rollback(ctx))
	require.Equal(t, 409, rec.Code, rec.Body.String())
	require.EqualValues(t, 4, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v2))
}

func mustUUID(s string) pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.MustParse(s), Valid: true}
}

func TestIntegrationBulkStatusHidesProduct(t *testing.T) {
	e := newOpsEnv(t)
	ctx := tenant.WithTenant(context.Background(), e.tenA)
	tID := mustUUID(e.tenA)
	pub := e.product(e.tenA, "Published", 1000)
	pubV := e.variant(pub.ID, e.sku("PUB"), 1000, 10)
	other := e.product(e.tenA, "Other published", 1000)
	e.variant(other.ID, e.sku("OTH"), 1000, 10)
	draft := e.product(e.tenA, "Soon draft", 2000)
	draftV := e.variant(draft.ID, e.sku("DRF"), 2000, 10)
	late := e.product(e.tenA, "Added later", 3000)
	lateV := e.variant(late.ID, e.sku("LATE"), 3000, 10)

	userID := e.user
	cartSvc := &cart.Service{Q: e.q, Pool: e.pool}
	c, err := cartSvc.EnsureCart(ctx, &userID, nil)
	require.NoError(t, err)
	cartID := cart.UUIDString(c.ID)
	require.NoError(t, cartSvc.AddItem(ctx, cartID, pub.ID, &pubV, 1))
	require.NoError(t, cartSvc.AddItem(ctx, cartID, draft.ID, &draftV, 1)) // added before the status change

	// Favourite, flash sale item and recommendation signals for the product.
	bg := context.Background()
	_, err = e.pool.Exec(bg, `INSERT INTO favorites(tenant_id,user_id,product_id) VALUES($1,$2,$3)`, e.tenA, e.user, draft.ID)
	require.NoError(t, err)
	var campaignID string
	require.NoError(t, e.pool.QueryRow(bg, `INSERT INTO flash_sale_campaigns(tenant_id,name,slug,status,starts_at,ends_at) VALUES($1,'IT','it-camp-'||$2,'ACTIVE',now()-interval '1 hour',now()+interval '1 day') RETURNING id::text`, e.tenA, randHex(4)).Scan(&campaignID))
	for _, pid := range []string{pub.ID, draft.ID} {
		_, err = e.pool.Exec(bg, `INSERT INTO flash_sale_items(campaign_id,product_id,sale_price) VALUES($1,$2,500)`, campaignID, pid)
		require.NoError(t, err)
	}
	for _, pair := range [][2]string{{pub.ID, draft.ID}, {pub.ID, other.ID}} {
		_, err = e.pool.Exec(bg, `INSERT INTO order_product_pairs(tenant_id,product_id_a,product_id_b,pair_count) VALUES($1,$2,$3,5)`, e.tenA, pair[0], pair[1])
		require.NoError(t, err)
	}
	for _, pid := range []string{pub.ID, draft.ID, other.ID} {
		_, err = e.pool.Exec(bg, `INSERT INTO user_product_views(tenant_id,user_id,product_id) VALUES($1,$2,$3)`, e.tenA, e.user, pid)
		require.NoError(t, err)
	}
	var voucherCode = "IT" + e.tag
	_, err = e.pool.Exec(bg, `INSERT INTO vouchers(code,value,tenant_id) VALUES($1,100,$2)`, voucherCode, e.tenA)
	require.NoError(t, err)

	rec := e.postJSON("/admin/products/bulk/status", e.tenA, map[string]any{"items": []map[string]any{{"productId": draft.ID, "status": "draft"}, {"productId": late.ID, "status": "archived"}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 2, e.scalar(`SELECT COUNT(*) FROM domain_events WHERE tenant_id=$1 AND topic='product.status_changed'`, e.tenA))

	listed, err := e.q.ListProductsPublic(ctx, dbgen.ListProductsPublicParams{TenantID: tID, Sort: "newest", LimitValue: 50})
	require.NoError(t, err)
	for _, p := range listed {
		require.NotEqual(t, draft.Slug, p.Slug)
		require.NotEqual(t, late.Slug, p.Slug)
	}
	require.Len(t, listed, 2)
	count, err := e.q.CountProductsPublic(ctx, dbgen.CountProductsPublicParams{TenantID: tID})
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	_, err = e.q.GetProductBySlug(ctx, dbgen.GetProductBySlugParams{Slug: draft.Slug, TenantID: tID})
	require.ErrorIs(t, err, pgx.ErrNoRows)
	favs, err := e.q.ListFavorites(ctx, dbgen.ListFavoritesParams{UserID: mustUUID(e.user), TenantID: tID})
	require.NoError(t, err)
	require.Empty(t, favs)
	fbt, err := e.q.GetFrequentlyBoughtTogether(ctx, dbgen.GetFrequentlyBoughtTogetherParams{TenantID: tID, ProductIDA: mustUUID(pub.ID), Limit: 10})
	require.NoError(t, err)
	require.Len(t, fbt, 1)
	require.Equal(t, other.Slug, fbt[0].Slug)
	cav, err := e.q.GetCustomersAlsoViewed(ctx, dbgen.GetCustomersAlsoViewedParams{TenantID: tID, ProductID: mustUUID(pub.ID), Limit: 10})
	require.NoError(t, err)
	require.Len(t, cav, 1)
	require.Equal(t, other.Slug, cav[0].Slug)
	flash := e.do(http.MethodGet, "/flash-sales", e.tenA, nil, "")
	require.Equal(t, 200, flash.Code, flash.Body.String())
	require.Contains(t, flash.Body.String(), pub.ID)
	require.NotContains(t, flash.Body.String(), draft.ID)

	// Add-to-cart is refused; voucher evaluation on the cart still works.
	err = cartSvc.AddItem(ctx, cartID, late.ID, &lateV, 1)
	require.ErrorIs(t, err, cart.ErrInvalidInput)
	_, _, err = cartSvc.EvaluateVoucher(ctx, c.ID, voucherCode)
	require.NoError(t, err)

	// Checkout of the cart with the stale draft line: 409, nothing reserved or ordered.
	var addrID string
	require.NoError(t, e.pool.QueryRow(bg, `INSERT INTO addresses(user_id,receiver_name) VALUES($1,'IT') RETURNING id::text`, e.user).Scan(&addrID))
	ordersBefore := e.scalar(`SELECT COUNT(*) FROM orders WHERE tenant_id=$1`, e.tenA)
	co := &checkout.Service{Q: e.q, Pool: e.pool, CartSvc: cartSvc}
	_, err = co.Create(ctx, &userID, checkout.Input{CartID: cartID, ShippingAddressID: addrID, ShippingService: "jne", PaymentMethod: "bank_transfer"})
	var appErr *common.AppError
	require.True(t, errors.As(err, &appErr), "got %v", err)
	require.Equal(t, "PRODUCT_UNAVAILABLE", appErr.Code)
	require.Equal(t, http.StatusConflict, appErr.HTTPStatus)
	require.Equal(t, ordersBefore, e.scalar(`SELECT COUNT(*) FROM orders WHERE tenant_id=$1`, e.tenA))
	require.EqualValues(t, 10, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, draftV))
	require.EqualValues(t, 10, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, pubV))
}

func TestIntegrationProductWebhooksAreTenantScoped(t *testing.T) {
	e := newOpsEnv(t)
	bg := context.Background()
	endpoint := func(tenantID string) string {
		var id string
		require.NoError(t, e.pool.QueryRow(bg, `INSERT INTO webhook_endpoints(tenant_id,name,url,secret,topics,active) VALUES($1,'it','https://example.test/hook','s','{}',true) RETURNING id::text`, tenantID).Scan(&id))
		return id
	}
	epA, epB := endpoint(e.tenA), endpoint(e.tenB)
	deliveries := func(ep string) int64 {
		return e.scalar(`SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id=$1`, ep)
	}
	p := e.product(e.tenA, "Hook", 100)
	e.variant(p.ID, e.sku("H1"), 100, 1)

	rec := e.postJSON("/admin/products/bulk/price", e.tenA, map[string]any{"items": []map[string]any{{"productId": p.ID, "price": 200}}})
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 2, deliveries(epA), "product price + variant price")
	require.EqualValues(t, 0, deliveries(epB), "tenant B never receives tenant A's product events")
	require.EqualValues(t, 0, e.scalar(`SELECT COUNT(*) FROM domain_events WHERE topic LIKE 'product.%' AND tenant_id IS DISTINCT FROM $1::uuid AND payload->>'productId' = $2`, e.tenA, p.ID))

	// Legacy emitters (payment callbacks run under the default tenant's context
	// while the order belongs to another tenant) keep the NULL-tenant broadcast:
	// the order's tenant (B here) still receives order.paid.
	callbackCtx := tenant.WithTenant(bg, e.tenA)
	ev, err := e.bus.Emit(callbackCtx, events.TopicOrderPaid, mustUUID(uuid.NewString()), map[string]any{"orderId": "it", "itTag": e.tag})
	require.NoError(t, err)
	require.False(t, ev.TenantID.Valid)
	require.EqualValues(t, 1, e.scalar(`SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id=$1 AND event_id=$2`, epB, ev.ID))
	require.EqualValues(t, 1, e.scalar(`SELECT COUNT(*) FROM webhook_deliveries WHERE endpoint_id=$1 AND event_id=$2`, epA, ev.ID))
}

func TestIntegrationProductsExport(t *testing.T) {
	e := newOpsEnv(t)
	bare := e.product(e.tenA, "Tanpa Varian", 500)
	p1 := e.product(e.tenA, `=HYPERLINK("http://x","y")`, 1000)
	e.variant(p1.ID, e.sku("E1"), 1000, 1)
	p2 := e.product(e.tenA, "Sepatu Olahraga – Ukuran 42", 2000)
	for i := 0; i < 3; i++ {
		e.variant(p2.ID, e.sku(fmt.Sprintf("E2-%d", i)), 2000, int32(i))
	}
	p3 := e.product(e.tenA, "Kopi Luwak ☕", 3000)
	e.variant(p3.ID, e.sku("E3"), 3000, 9)
	e.variant(p3.ID, "", 3000, 4) // SKU-less variant
	e.product(e.tenB, "Tenant B secret", 1)

	export := func(batch int, query string) *httptest.ResponseRecorder {
		e.catalog.ExportBatchSize = batch
		rec := e.do(http.MethodGet, "/admin/products/export"+query, e.tenA, nil, "")
		require.Equal(t, 200, rec.Code, rec.Body.String())
		return rec
	}
	rec := export(2, "")
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Regexp(t, `^attachment; filename="products-\d{8}-\d{6}\.csv"$`, rec.Header().Get("Content-Disposition"))
	rows := readCSV(t, rec.Body.Bytes())
	require.Equal(t, "sku,product_id,variant_id,title,slug,status,price,compare_at,variant_price,stock,stock_baseline,low_stock_threshold,in_stock,stock_status,category_slug,brand_slug,description,attributes,created_at,updated_at", strings.Join(rows[0], ","))
	require.Len(t, rows, 1+1+1+3+2, "one row per variant plus the variant-less product")
	col := colIndex(rows[0])
	seen := map[string]int{}
	titles := map[string]string{}
	for _, row := range rows[1:] {
		if row[col["variant_id"]] != "" {
			seen[row[col["variant_id"]]]++
		}
		titles[row[col["product_id"]]] = row[col["title"]]
		require.NotEqual(t, "Tenant B secret", row[col["title"]])
		if row[col["product_id"]] == bare.ID {
			require.Equal(t, "", row[col["sku"]])
			require.Equal(t, "", row[col["stock"]])
		}
	}
	require.Len(t, seen, 6)
	for _, n := range seen {
		require.Equal(t, 1, n)
	}
	require.Equal(t, `'=HYPERLINK("http://x","y")`, titles[p1.ID])
	require.Equal(t, "Sepatu Olahraga – Ukuran 42", titles[p2.ID])
	require.Equal(t, "Kopi Luwak ☕", titles[p3.ID])

	body2 := rows
	for _, batch := range []int{1, 500} {
		require.Equal(t, body2, readCSV(t, export(batch, "").Body.Bytes()), "batch size %d", batch)
	}
	filtered := readCSV(t, export(500, "?stockStatus=out").Body.Bytes())
	require.Len(t, filtered, 1, "no product is out of stock (only the header)")
	require.Equal(t, 400, e.do(http.MethodGet, "/admin/products/export?sort=bogus", e.tenA, nil, "").Code)
}

func TestIntegrationOrdersExport(t *testing.T) {
	e := newOpsEnv(t)
	bg := context.Background()
	var customer string
	require.NoError(t, e.pool.QueryRow(bg, `INSERT INTO users(name,email,password_hash) VALUES('=cmd|calc',$1,'x') RETURNING id::text`, "it-c-"+randHex(4)+"@example.test").Scan(&customer))
	t.Cleanup(func() {
		_, _ = e.pool.Exec(bg, `DELETE FROM orders WHERE user_id=$1`, customer)
		_, _ = e.pool.Exec(bg, `DELETE FROM users WHERE id=$1`, customer)
	})
	order := func(tenantID, number, status, created string) {
		_, err := e.pool.Exec(bg, `INSERT INTO orders(tenant_id,user_id,order_number,status,pricing_subtotal,pricing_total,created_at) VALUES($1,$2,$3,$4,1000,1100,$5)`, tenantID, customer, number, status, created)
		require.NoError(t, err)
	}
	order(e.tenA, "IT-1", "PAID", "2026-03-01T23:59:59Z")
	order(e.tenA, "IT-2", "PENDING_PAYMENT", "2026-03-02T00:00:00Z")
	order(e.tenA, "IT-3", "PAID", "2026-03-03T10:00:00Z")
	order(e.tenB, "IT-B", "PAID", "2026-03-02T05:00:00Z")

	numbers := func(query string, batch int) []string {
		e.orders.ExportBatchSize = batch
		rec := e.do(http.MethodGet, "/admin/orders/export"+query, e.tenA, nil, "")
		require.Equal(t, 200, rec.Code, rec.Body.String())
		rows := readCSV(t, rec.Body.Bytes())
		require.Equal(t, "order_number", rows[0][0])
		var out []string
		col := colIndex(rows[0])
		for _, r := range rows[1:] {
			out = append(out, r[col["order_number"]])
			require.Equal(t, "'=cmd|calc", r[col["customer_name"]])
			require.Equal(t, "1100", r[col["total"]])
		}
		return out
	}
	require.Equal(t, []string{"IT-3", "IT-2", "IT-1"}, numbers("", 2))
	require.Equal(t, []string{"IT-3", "IT-2", "IT-1"}, numbers("", 1))
	require.Equal(t, []string{"IT-3", "IT-1"}, numbers("?status=PAID", 1000))
	// UTC day boundaries: startDate inclusive, endDate exclusive.
	require.Equal(t, []string{"IT-2"}, numbers("?startDate=2026-03-02&endDate=2026-03-03", 1000))
	require.Equal(t, []string{"IT-1"}, numbers("?endDate=2026-03-02", 1000))
	require.Equal(t, 400, e.do(http.MethodGet, "/admin/orders/export?status=NOPE", e.tenA, nil, "").Code)
}

func TestIntegrationImport(t *testing.T) {
	e := newOpsEnv(t)
	bg := context.Background()
	_, err := e.pool.Exec(bg, `INSERT INTO categories(tenant_id,name,slug) VALUES($1,'Sepatu','it-sepatu-'||$2)`, e.tenA, strings.ToLower(e.tag))
	require.NoError(t, err)
	catSlug := "it-sepatu-" + strings.ToLower(e.tag)
	shoe := e.product(e.tenA, "Sepatu Lari", 10000)
	s1 := e.variant(shoe.ID, e.sku("S1"), 10000, 10)
	e.variant(shoe.ID, e.sku("S2"), 10000, 5)
	skuLess := e.variant(shoe.ID, "", 10000, 2)
	noVariant := e.product(e.tenA, "Tanpa Varian", 700)
	// CR characters do not survive a CSV round trip (CRLF reads back as LF, a
	// lone CR is dropped); an unchanged re-import must still be a no-op.
	_, err = e.pool.Exec(bg, `UPDATE products SET description = E'Baris satu\r\nBaris dua\rtiga' WHERE id = $1`, noVariant.ID)
	require.NoError(t, err)
	bProd := e.product(e.tenB, "B product", 1)
	e.variant(bProd.ID, e.sku("B1"), 1, 1)
	_, err = e.pool.Exec(bg, `UPDATE products SET slug=$1 WHERE id=$2`, "it-dup-"+strings.ToLower(e.tag), bProd.ID)
	require.NoError(t, err)
	bChecksum := e.catalogChecksum(e.tenB)

	exportFile := func() []byte {
		rec := e.do(http.MethodGet, "/admin/products/export", e.tenA, nil, "")
		require.Equal(t, 200, rec.Code)
		return rec.Body.Bytes()
	}

	// 1. Export -> re-import round trip (SKU-less variant, product without
	// variants, non-ASCII) is all unchanged, in both modes, with no writes.
	original := exportFile()
	before := e.catalogChecksum(e.tenA)
	for _, dry := range []bool{true, false} {
		rec := e.importCSV(e.tenA, "products", dry, original)
		require.Equal(t, 200, rec.Code, rec.Body.String())
		res := decode[importResp](t, rec)
		require.Equal(t, 4, res.Data.Summary.Total)
		require.Equal(t, 4, res.Data.Summary.Unchanged, rec.Body.String())
	}
	require.Equal(t, before, e.catalogChecksum(e.tenA))
	require.EqualValues(t, 0, e.auditCount("catalog.import.commit"))

	// 2. Header failures: zero writes, 400.
	for name, data := range map[string]string{
		"missing": "variant_id,stock\n" + s1 + ",1\n",
		"renamed": "sku,stok\nX,1\n",
		"dup":     "sku,sku\nX,Y\n",
	} {
		rec := e.importCSV(e.tenA, "products", false, []byte(data))
		require.Equal(t, 400, rec.Code, name)
	}

	// 3. Planned change set, dry run then commit.
	desc := "'=Deskripsi baru" // escaped formula-like text, as an export writes it
	pro := "Sepatu Lari Pro"
	planCSV := string(writeCSV(t, [][]string{
		{"sku", "variant_id", "product_id", "title", "slug", "price", "variant_price", "stock", "stock_baseline", "description", "category_slug", "attributes"},
		{e.sku("S1"), "", "", pro, "", "10000", "12000", "15", "10", desc, "", `{"size":"43"}`},
		{e.sku("S2"), "", "", pro, "", "10000", "", "", "", desc, "", ""},
		{e.sku("S3"), "", shoe.ID, pro, "", "10000", "9000", "3", "", desc, "", ""},
		{e.sku("N1"), "", "", "Sandal Gunung", "", "20000", "", "7", "", "", catSlug, ""},
		{e.sku("N2"), "", "", "Sandal Gunung", "", "20000", "21000", "1", "", "", catSlug, ""},
		{e.sku("D1"), "", "", "IT Dup " + strings.ToLower(e.tag), "", "500", "", "", "", "", "", ""},
	}))
	rec := e.importCSV(e.tenA, "products", true, []byte(planCSV))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	dry := decode[importResp](t, rec)
	require.Equal(t, before, e.catalogChecksum(e.tenA), "dry run writes nothing")
	require.Equal(t, []string{"update", "unchanged", "create_variant", "create", "create", "create"}, actions(dry))
	require.Equal(t, dry.Data.Rows[3].Slug, dry.Data.Rows[4].Slug, "one new product for both rows")
	require.Equal(t, "it-dup-"+strings.ToLower(e.tag)+"-2", dry.Data.Rows[5].Slug, "derived slug taken by tenant B gets -2")
	row0 := dry.Data.Rows[0]
	require.Equal(t, "Sepatu Lari Pro", row0.Changes["title"]["to"])
	require.Equal(t, "=Deskripsi baru", row0.Changes["description"]["to"])
	require.EqualValues(t, 15, row0.Changes["stock"]["to"])

	// Dry run left no row locks: a concurrent reservation proceeds at once.
	tx, err := e.pool.Begin(bg)
	require.NoError(t, err)
	_, err = tx.Exec(bg, `SET LOCAL lock_timeout = '1s'`)
	require.NoError(t, err)
	_, err = dbgen.New(tx).ReserveVariantStock(bg, dbgen.ReserveVariantStockParams{ID: mustUUID(s1), Qty: 1})
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(bg))

	eventsBefore := e.productEvents(e.tenA)
	rec = e.importCSV(e.tenA, "products", false, []byte(planCSV))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 15, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, s1))
	require.EqualValues(t, 12000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, s1))
	var title, storedDesc, attrs string
	require.NoError(t, e.pool.QueryRow(bg, `SELECT title, description FROM products WHERE id=$1`, shoe.ID).Scan(&title, &storedDesc))
	require.Equal(t, "Sepatu Lari Pro", title)
	require.Equal(t, "=Deskripsi baru", storedDesc, "formula-like text is stored literally")
	require.NoError(t, e.pool.QueryRow(bg, `SELECT attributes::text FROM product_variants WHERE id=$1`, s1).Scan(&attrs))
	require.JSONEq(t, `{"size":"43"}`, attrs)
	require.EqualValues(t, 1, e.scalar(`SELECT COUNT(*) FROM product_variants WHERE sku=$1`, e.sku("S3")))
	require.EqualValues(t, 2, e.scalar(`SELECT COUNT(*) FROM product_variants v JOIN products p ON p.id=v.product_id WHERE p.tenant_id=$1 AND p.title='Sandal Gunung'`, e.tenA))
	require.EqualValues(t, 1, e.scalar(`SELECT COUNT(*) FROM products WHERE tenant_id=$1 AND title='Sandal Gunung' AND category_id IS NOT NULL`, e.tenA))
	require.EqualValues(t, 2, e.productEvents(e.tenA)-eventsBefore, "stock + variant price of the updated variant")
	require.EqualValues(t, 1, e.auditCount("catalog.import.commit"))
	var meta string
	require.NoError(t, e.pool.QueryRow(bg, `SELECT metadata::text FROM audit_logs WHERE action='catalog.import.commit' AND actor_user_id=$1::uuid`, e.user).Scan(&meta))
	require.Contains(t, meta, `"title": {"to": "Sepatu Lari Pro", "from": "Sepatu Lari"}`)
	require.Contains(t, meta, `"description": {"to": "=Deskripsi baru", "from": null}`)
	require.Equal(t, bChecksum, e.catalogChecksum(e.tenB), "tenant B untouched")

	// 4. Same file again: all unchanged / no new events / no new audit row.
	// Created rows now resolve by SKU as updates without changes, and the
	// edited stock cell (15) equals the live stock.
	afterCommit := e.catalogChecksum(e.tenA)
	rec = e.importCSV(e.tenA, "products", false, []byte(planCSV))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	again := decode[importResp](t, rec)
	require.Equal(t, 6, again.Data.Summary.Unchanged, rec.Body.String())
	require.Equal(t, afterCommit, e.catalogChecksum(e.tenA))
	require.EqualValues(t, 2, e.productEvents(e.tenA)-eventsBefore)
	require.EqualValues(t, 1, e.auditCount("catalog.import.commit"))

	// 5. One bad row at line 7 rejects the whole file and is audited.
	bad := "sku,stock\n" + e.sku("S1") + ",1\n" + e.sku("S2") + ",2\n" + e.sku("S3") + ",3\n" + e.sku("N1") + ",4\n" + e.sku("N2") + ",5\n" + e.sku("S1") + "X,abc\n"
	rec = e.importCSV(e.tenA, "stock", false, []byte(bad))
	require.Equal(t, 422, rec.Code, rec.Body.String())
	rej := importRespFrom422(t, rec)
	require.Equal(t, 1, rej.Data.Summary.Rejected)
	require.Equal(t, 7, rej.Data.Rows[5].Row)
	require.Equal(t, afterCommit, e.catalogChecksum(e.tenA))
	require.EqualValues(t, 1, e.auditCount("catalog.import.rejected"))
	// Retry with the row fixed: applied once.
	rec = e.importCSV(e.tenA, "stock", false, []byte(strings.Replace(bad, e.sku("S1")+"X,abc\n", "", 1)))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 1, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, s1))

	// 6. Identity errors.
	rec = e.importCSV(e.tenA, "products", true, []byte("sku,title,price\n"+e.sku("B1")+",Steal,1\n"))
	require.Equal(t, 422, rec.Code)
	require.Equal(t, "SKU_TAKEN", importRespFrom422(t, rec).Data.Rows[0].Errors[0].Code)
	rec = e.importCSV(e.tenA, "products", true, []byte("sku,variant_id\n1.23E+12,"+s1+"\n"))
	require.Equal(t, 422, rec.Code)
	require.Equal(t, "SKU_MISMATCH", importRespFrom422(t, rec).Data.Rows[0].Errors[0].Code)
	rec = e.importCSV(e.tenA, "products", true, []byte("sku,variant_id\n,"+bProdVariant(t, e, bProd.ID)+"\n"))
	require.Equal(t, 422, rec.Code)
	require.Equal(t, "UNKNOWN_VARIANT", importRespFrom422(t, rec).Data.Rows[0].Errors[0].Code)

	// 7. SKU-less variant gets a SKU assigned through variant_id.
	rec = e.importCSV(e.tenA, "products", false, []byte("sku,variant_id\n"+e.sku("S0")+","+skuLess+"\n"))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 1, e.scalar(`SELECT COUNT(*) FROM product_variants WHERE id=$1 AND sku=$2`, skuLess, e.sku("S0")))
}

func bProdVariant(t *testing.T, e *opsEnv, productID string) string {
	var id string
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT id::text FROM product_variants WHERE product_id=$1 LIMIT 1`, productID).Scan(&id))
	return id
}

func actions(res importResp) []string {
	out := make([]string, 0, len(res.Data.Rows))
	for _, r := range res.Data.Rows {
		out = append(out, r.Action)
	}
	return out
}

// Sales between export and import must never be overwritten by the stale
// stock value of the exported file.
func TestIntegrationImportDoesNotRestoreSoldStock(t *testing.T) {
	e := newOpsEnv(t)
	bg := context.Background()
	p := e.product(e.tenA, "Jaket", 50000)
	v := e.variant(p.ID, e.sku("J1"), 50000, 10)

	rec := e.do(http.MethodGet, "/admin/products/export", e.tenA, nil, "")
	require.Equal(t, 200, rec.Code)
	file := rec.Body.Bytes()

	// Three units sell after the export.
	_, err := e.q.ReserveVariantStock(bg, dbgen.ReserveVariantStockParams{ID: mustUUID(v), Qty: 3})
	require.NoError(t, err)

	// Admin edits only the price and re-imports: stock stays 7.
	rows := readCSV(t, file)
	col := colIndex(rows[0])
	rows[1][col["price"]] = "55000"
	rows[1][col["variant_price"]] = "55000"
	rec = e.importCSV(e.tenA, "products", false, writeCSV(t, rows))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 7, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v))
	require.EqualValues(t, 55000, e.scalar(`SELECT price FROM product_variants WHERE id=$1`, v))

	// Editing the stale stock cell is rejected, nothing changes.
	rows[1][col["stock"]] = "20"
	rec = e.importCSV(e.tenA, "products", false, writeCSV(t, rows))
	require.Equal(t, 422, rec.Code, rec.Body.String())
	require.Equal(t, "STALE_STOCK", importRespFrom422(t, rec).Data.Rows[0].Errors[0].Code)
	require.EqualValues(t, 7, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v))

	// Same rule in type=stock when the baseline column is present.
	rec = e.importCSV(e.tenA, "stock", false, []byte("sku,stock,stock_baseline\n"+e.sku("J1")+",20,10\n"))
	require.Equal(t, 422, rec.Code, rec.Body.String())
	rec = e.importCSV(e.tenA, "stock", false, []byte("sku,stock,stock_baseline\n"+e.sku("J1")+",20,7\n"))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.EqualValues(t, 20, e.scalar(`SELECT stock FROM product_variants WHERE id=$1`, v))
}

// TestIntegrationImportBodyLimits sends uploads through the same global
// BodyLimit as cmd/api (1 MiB default plus admin.BodyLimitOverrides): an
// exported catalog between 1 MiB and 5 MiB re-imports, a file over 5 MiB gets
// the handler's JSON PAYLOAD_TOO_LARGE, and bulk bodies over the global cap
// get the JSON envelope from the middleware.
func TestIntegrationImportBodyLimits(t *testing.T) {
	e := newOpsEnv(t)
	bg := context.Background()
	p := e.product(e.tenA, "Kaos Polos", 45000)
	_, err := e.pool.Exec(bg, `UPDATE products SET description = repeat('Bahan katun combed 30s, nyaman dipakai. ', 25) WHERE id = $1`, p.ID)
	require.NoError(t, err)
	_, err = e.pool.Exec(bg, `
		INSERT INTO product_variants(product_id, sku, price, stock, attributes)
		SELECT $1, $2 || '-L' || g, 45000, 5, jsonb_build_object('size', 'S' || g)
		FROM generate_series(1, 1500) g`, p.ID, e.tag)
	require.NoError(t, err)

	rec := e.do(http.MethodGet, "/admin/products/export", e.tenA, nil, "")
	require.Equal(t, 200, rec.Code)
	file := rec.Body.Bytes()
	require.Greater(t, len(file), productionBodyLimit, "fixture must exceed the global 1 MiB cap")
	require.Less(t, len(file), 5<<20)

	before := e.catalogChecksum(e.tenA)
	rec = e.importCSV(e.tenA, "products", true, file)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	res := decode[importResp](t, rec)
	require.Equal(t, 1500, res.Data.Summary.Total)
	require.Equal(t, 1500, res.Data.Summary.Unchanged)
	require.Equal(t, before, e.catalogChecksum(e.tenA))

	// Over 5 MiB: rejected by the handler with the JSON envelope, no writes.
	big := append([]byte("sku,stock\n"), bytes.Repeat([]byte("X,1\n"), (5<<20)/4+10)...)
	rec = e.importCSV(e.tenA, "stock", false, big)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, "PAYLOAD_TOO_LARGE", decode[apiError](t, rec).Error.Code)
	require.Equal(t, before, e.catalogChecksum(e.tenA))

	// Bulk endpoints keep the global cap, answered as JSON.
	huge := []byte(`{"items":[` + strings.Repeat(`{"sku":"x","stock":1},`, productionBodyLimit/20) + `{"sku":"x","stock":1}]}`)
	rec = e.do(http.MethodPost, "/admin/products/bulk/stock", e.tenA, huge, "application/json")
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, "PAYLOAD_TOO_LARGE", decode[apiError](t, rec).Error.Code)
}

func writeCSV(t *testing.T, rows [][]string) []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	require.NoError(t, w.WriteAll(rows))
	return buf.Bytes()
}

func TestIntegrationLowStock(t *testing.T) {
	e := newOpsEnv(t)
	bg := context.Background()
	low := e.product(e.tenA, "Low", 100)
	lowV := e.variant(low.ID, e.sku("L"), 100, 3)
	out := e.product(e.tenA, "Out", 100)
	e.variant(out.ID, e.sku("O"), 100, 0)
	plenty := e.product(e.tenA, "Plenty", 100)
	e.variant(plenty.ID, e.sku("P"), 100, 50)

	type invResp struct {
		Data []struct {
			VariantID         string `json:"variantId"`
			StockStatus       string `json:"stockStatus"`
			LowStockThreshold int    `json:"lowStockThreshold"`
		} `json:"data"`
	}
	inventory := func(query string) map[string]string {
		rec := e.do(http.MethodGet, "/admin/inventory"+query, e.tenA, nil, "")
		require.Equal(t, 200, rec.Code, rec.Body.String())
		m := map[string]string{}
		for _, it := range decode[invResp](t, rec).Data {
			m[it.VariantID] = it.StockStatus
		}
		return m
	}
	overview := func() (int, int) {
		rec := e.do(http.MethodGet, "/admin/analytics/overview", e.tenA, nil, "")
		require.Equal(t, 200, rec.Code, rec.Body.String())
		var resp struct {
			Data struct{ LowStockProducts, OutOfStockProducts int } `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		return resp.Data.LowStockProducts, resp.Data.OutOfStockProducts
	}
	require.Len(t, inventory("?lowStock=true"), 1)
	require.Equal(t, "low", inventory("?stockStatus=low")[lowV])
	require.Len(t, inventory("?stockStatus=out"), 1)
	require.Len(t, inventory("?stockStatus=in"), 1)
	require.Equal(t, 400, e.do(http.MethodGet, "/admin/inventory?stockStatus=bad", e.tenA, nil, "").Code)
	l, o := overview()
	require.Equal(t, 1, l)
	require.Equal(t, 1, o)

	rec := e.do(http.MethodPatch, "/admin/settings", e.tenA, []byte(`{"inventory":{"lowStockThreshold":60}}`), "application/json")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Len(t, inventory("?lowStock=true"), 2)
	l, _ = overview()
	require.Equal(t, 2, l)
	require.Equal(t, 400, e.do(http.MethodPatch, "/admin/settings", e.tenA, []byte(`{"inventory":{"lowStockThreshold":-1}}`), "application/json").Code)

	// A product override beats the tenant default.
	_, err := e.pool.Exec(bg, `UPDATE products SET low_stock_threshold=2 WHERE id=$1`, low.ID)
	require.NoError(t, err)
	require.Equal(t, "in", inventory("")[lowV])
	l, _ = overview()
	require.Equal(t, 1, l)
}
