package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/admin"
	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/order"
	"github.com/noah-isme/backend-toko/internal/rbac"
	"github.com/noah-isme/backend-toko/internal/shipping"
	"github.com/noah-isme/backend-toko/internal/tenant"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

// These tests drive the real admin router (registerAdminRoutes) against
// Postgres. They prove that an OWNER of a self-made tenant A — which anyone can
// become via POST /onboarding — cannot reach tenant B's rows or the
// platform-global resources. Opt-in: set TEST_DATABASE_URL.

type isolationITest struct {
	t       *testing.T
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	router  http.Handler
	tenA    string
	tenB    string
	owner   string
	buyer   string
	orderA  string
	orderB  string
	voucher map[string]string // code -> tenant
}

func randSuffix() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newIsolationITest(t *testing.T) *isolationITest {
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

	e := &isolationITest{t: t, pool: pool, q: dbgen.New(pool), voucher: map[string]string{}}
	e.tenA = e.scalar(`INSERT INTO tenants(slug,name) VALUES($1,'Iso A') RETURNING id::text`, "iso-a-"+randSuffix())
	e.tenB = e.scalar(`INSERT INTO tenants(slug,name) VALUES($1,'Iso B') RETURNING id::text`, "iso-b-"+randSuffix())
	e.owner = e.scalar(`INSERT INTO users(name,email,password_hash,roles) VALUES('Iso owner',$1,'x','{customer}') RETURNING id::text`, "iso-owner-"+randSuffix()+"@example.test")
	e.buyer = e.scalar(`INSERT INTO users(name,email,password_hash,roles) VALUES('Iso buyer',$1,'x','{customer}') RETURNING id::text`, "iso-buyer-"+randSuffix()+"@example.test")
	t.Cleanup(func() {
		cctx := context.Background()
		tenants := []string{e.tenA, e.tenB}
		for _, stmt := range []string{
			`DELETE FROM shipments WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM voucher_usages WHERE voucher_id IN (SELECT id FROM vouchers WHERE tenant_id = ANY($1::uuid[]))`,
			`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM carts WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM vouchers WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM brands WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM categories WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM tenants WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(cctx, stmt, tenants); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, []string{e.owner, e.buyer}); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})

	e.exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'OWNER','ACTIVE')`, e.tenA, e.owner)
	e.exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'CUSTOMER','ACTIVE')`, e.tenA, e.buyer)
	e.exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'CUSTOMER','ACTIVE')`, e.tenB, e.buyer)
	e.orderB = e.scalar(`INSERT INTO orders(user_id,status,pricing_subtotal,pricing_total,tenant_id) VALUES($1,'PENDING_PAYMENT',1000,1000,$2) RETURNING id::text`, e.buyer, e.tenB)
	e.orderA = e.scalar(`INSERT INTO orders(user_id,status,pricing_subtotal,pricing_total,tenant_id) VALUES($1,'PAID',1000,1000,$2) RETURNING id::text`, e.buyer, e.tenA)
	e.addVoucher("B-ONLY-"+randSuffix(), e.tenB)
	e.addVoucher("A-ONLY-"+randSuffix(), e.tenA)

	d := adminDeps{
		RequireAuth: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				if u := r.Header.Get("X-Test-User"); u != "" {
					ctx = common.WithUserID(ctx, u)
				}
				if tn := r.Header.Get("X-Test-Tenant"); tn != "" {
					ctx = tenant.WithTenant(ctx, tn)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		},
		Membership:    rbac.RequireMembership(e.q),
		PlatformAdmin: rbac.RequirePlatformAdmin(e.q),
		Orders:        &order.AdminHandler{Q: e.q},
		Shipping:      &shipping.Handler{Svc: &shipping.Service{Q: e.q}, Q: e.q},
		Vouchers:      &voucher.Handler{Q: e.q, Svc: &voucher.Service{Q: e.q}, Pool: pool},
		Staff:         &rbac.StaffHandler{Pool: pool, Q: e.q},
		Catalog:       &admin.CatalogHandler{Q: e.q},
		// Platform handlers stay nil: they are nil-safe (500) and only the
		// guard outcome matters here.
	}
	r := chi.NewRouter()
	r.Route("/api/v1/admin", func(a chi.Router) { registerAdminRoutes(a, d) })
	r.Route("/api/v1/analytics", func(a chi.Router) { registerAnalyticsRoutes(a, d) })
	e.router = r
	return e
}

func (e *isolationITest) scalar(sql string, args ...any) string {
	e.t.Helper()
	var out string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&out))
	return out
}

func (e *isolationITest) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

func (e *isolationITest) addVoucher(code, tenantID string) {
	e.exec(`INSERT INTO vouchers(code,value,kind,tenant_id) VALUES($1,500,'fixed_amount',$2)`, code, tenantID)
	e.voucher[code] = tenantID
}

func (e *isolationITest) code(tenantID string) string {
	for c, tn := range e.voucher {
		if tn == tenantID && (strings.HasPrefix(c, "A-ONLY") || strings.HasPrefix(c, "B-ONLY")) {
			return c
		}
	}
	e.t.Fatalf("no voucher for tenant %s", tenantID)
	return ""
}

func (e *isolationITest) do(method, path, user, tenantID, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Test-User", user)
	req.Header.Set("X-Test-Tenant", tenantID)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func requiredOf(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body.Error.Code, body.Error.Details["required"]
}

func TestIntegrationAdminTenantIsolation(t *testing.T) {
	e := newIsolationITest(t)
	codeA, codeB := e.code(e.tenA), e.code(e.tenB)

	t.Run("order status of another tenant is 404 and unchanged", func(t *testing.T) {
		// PENDING_PAYMENT -> CANCELLED is an allowed transition, so without
		// the tenant predicate this would really cancel B's order.
		rec := e.do(http.MethodPatch, "/api/v1/admin/orders/"+e.orderB+"/status", e.owner, e.tenA, `{"status":"CANCELLED"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Equal(t, "PENDING_PAYMENT", e.scalar(`SELECT status::text FROM orders WHERE id=$1`, e.orderB))
	})

	t.Run("shipment for another tenant's order is 404", func(t *testing.T) {
		e.exec(`UPDATE orders SET status='PAID' WHERE id=$1`, e.orderB)
		defer e.exec(`UPDATE orders SET status='PENDING_PAYMENT' WHERE id=$1`, e.orderB)
		rec := e.do(http.MethodPost, "/api/v1/admin/orders/"+e.orderB+"/shipment", e.owner, e.tenA, `{"courier":"jne","trackingNumber":"T1"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Equal(t, "0", e.scalar(`SELECT COUNT(*)::text FROM shipments WHERE order_id=$1`, e.orderB))
		assert.Equal(t, "PAID", e.scalar(`SELECT status::text FROM orders WHERE id=$1`, e.orderB))
	})

	t.Run("voucher update and preview of another tenant are 404", func(t *testing.T) {
		rec := e.do(http.MethodPut, "/api/v1/admin/vouchers/"+codeB, e.owner, e.tenA, `{"code":"`+codeB+`","value":99999,"kind":"fixed_amount"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Equal(t, "500", e.scalar(`SELECT value::text FROM vouchers WHERE code=$1`, codeB))

		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers/preview", e.owner, e.tenA, `{"code":"`+codeB+`","cartTotal":10000,"items":[{"subtotal":10000}]}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})

	t.Run("product cannot reference another tenant's brand or category", func(t *testing.T) {
		sfx := randSuffix()
		brandB := e.scalar(`INSERT INTO brands(name,slug,tenant_id) VALUES($1,$1,$2) RETURNING id::text`, "iso-brand-b-"+sfx, e.tenB)
		categoryB := e.scalar(`INSERT INTO categories(name,slug,tenant_id) VALUES($1,$1,$2) RETURNING id::text`, "iso-cat-b-"+sfx, e.tenB)
		brandA := e.scalar(`INSERT INTO brands(name,slug,tenant_id) VALUES($1,$1,$2) RETURNING id::text`, "iso-brand-a-"+sfx, e.tenA)

		rec := e.do(http.MethodPost, "/api/v1/admin/products", e.owner, e.tenA, `{"title":"Iso P `+sfx+`","price":1000,"brandId":"`+brandB+`"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		rec = e.do(http.MethodPost, "/api/v1/admin/products", e.owner, e.tenA, `{"title":"Iso P `+sfx+`","price":1000,"categoryId":"`+categoryB+`"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Equal(t, "0", e.scalar(`SELECT COUNT(*)::text FROM products WHERE tenant_id=$1`, e.tenA))

		rec = e.do(http.MethodPost, "/api/v1/admin/products", e.owner, e.tenA, `{"title":"Iso P `+sfx+`","price":1000,"brandId":"`+brandA+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		productA := e.scalar(`SELECT id::text FROM products WHERE tenant_id=$1`, e.tenA)

		rec = e.do(http.MethodPatch, "/api/v1/admin/products/"+productA, e.owner, e.tenA, `{"categoryId":"`+categoryB+`"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Equal(t, "", e.scalar(`SELECT COALESCE(category_id::text,'') FROM products WHERE id=$1`, productA))
	})

	t.Run("category cannot have another tenant's category as parent", func(t *testing.T) {
		sfx := randSuffix()
		parentB := e.scalar(`INSERT INTO categories(name,slug,tenant_id) VALUES($1,$1,$2) RETURNING id::text`, "iso-parent-b-"+sfx, e.tenB)
		parentA := e.scalar(`INSERT INTO categories(name,slug,tenant_id) VALUES($1,$1,$2) RETURNING id::text`, "iso-parent-a-"+sfx, e.tenA)

		rec := e.do(http.MethodPost, "/api/v1/admin/categories", e.owner, e.tenA, `{"name":"Iso child `+sfx+`","parentId":"`+parentB+`"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Equal(t, "0", e.scalar(`SELECT COUNT(*)::text FROM categories WHERE parent_id=$1`, parentB))

		rec = e.do(http.MethodPost, "/api/v1/admin/categories", e.owner, e.tenA, `{"name":"Iso child `+sfx+`","parentId":"`+parentA+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		childA := e.scalar(`SELECT id::text FROM categories WHERE parent_id=$1`, parentA)

		rec = e.do(http.MethodPatch, "/api/v1/admin/categories/"+childA, e.owner, e.tenA, `{"parentId":"`+parentB+`"}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		assert.Equal(t, parentA, e.scalar(`SELECT COALESCE(parent_id::text,'') FROM categories WHERE id=$1`, childA))
	})

	t.Run("positive controls in own tenant", func(t *testing.T) {
		rec := e.do(http.MethodPatch, "/api/v1/admin/orders/"+e.orderA+"/status", e.owner, e.tenA, `{"status":"PACKED"}`)
		assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

		newCode := "A-NEW-" + randSuffix()
		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers", e.owner, e.tenA, `{"code":"`+newCode+`","value":100,"kind":"fixed_amount"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		e.voucher[newCode] = e.tenA
		assert.Equal(t, e.tenA, e.scalar(`SELECT tenant_id::text FROM vouchers WHERE code=$1`, newCode))

		rec = e.do(http.MethodPut, "/api/v1/admin/vouchers/"+codeA, e.owner, e.tenA, `{"code":"`+codeA+`","value":700,"kind":"fixed_amount"}`)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers/preview", e.owner, e.tenA, `{"code":"`+codeA+`","cartTotal":10000,"items":[{"subtotal":10000}]}`)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})

	t.Run("platform-global routes need the global admin role", func(t *testing.T) {
		paths := []string{"/api/v1/admin/webhooks", "/api/v1/admin/queue/stats", "/api/v1/admin/audit-logs"}
		for _, p := range paths {
			rec := e.do(http.MethodGet, p, e.owner, e.tenA, "")
			require.Equal(t, http.StatusForbidden, rec.Code, p)
			code, required := requiredOf(t, rec)
			assert.Equal(t, "FORBIDDEN", code, p)
			assert.Equal(t, "platform-admin", required, p)
		}
		e.exec(`UPDATE users SET roles = roles || '{admin}' WHERE id=$1`, e.owner)
		defer e.exec(`UPDATE users SET roles = '{customer}' WHERE id=$1`, e.owner)
		for _, p := range paths {
			rec := e.do(http.MethodGet, p, e.owner, e.tenA, "")
			assert.NotEqual(t, http.StatusForbidden, rec.Code, p)
		}
	})

	t.Run("tenant analytics need analytics:read, not the global admin role", func(t *testing.T) {
		// /analytics/* is tenant-scoped (rec-api-analytics), so a tenant owner
		// passes the guard without users.roles admin; a customer member does not.
		for _, p := range []string{"/api/v1/analytics/overview", "/api/v1/analytics/sales", "/api/v1/analytics/top-products"} {
			rec := e.do(http.MethodGet, p, e.owner, e.tenA, "")
			assert.NotEqual(t, http.StatusForbidden, rec.Code, p)
			rec = e.do(http.MethodGet, p, e.buyer, e.tenA, "")
			require.Equal(t, http.StatusForbidden, rec.Code, p)
			_, required := requiredOf(t, rec)
			assert.Equal(t, "staff", required, p)
		}
	})

	t.Run("non-member and customer member are denied", func(t *testing.T) {
		rec := e.do(http.MethodGet, "/api/v1/admin/products", e.owner, e.tenB, "")
		require.Equal(t, http.StatusForbidden, rec.Code)
		code, _ := requiredOf(t, rec)
		assert.Equal(t, "TENANT_FORBIDDEN", code)

		rec = e.do(http.MethodGet, "/api/v1/admin/products", e.buyer, e.tenA, "")
		require.Equal(t, http.StatusForbidden, rec.Code)
		code, required := requiredOf(t, rec)
		assert.Equal(t, "FORBIDDEN", code)
		assert.Equal(t, "staff", required)
	})
}

// TestIntegrationVoucherOfOtherTenantNotRedeemable covers the free-goods
// exploit: a voucher an owner creates in their own tenant A must not apply to
// a cart in tenant B, neither via apply-voucher nor at checkout (which prices
// through cart.Service.EvaluateVoucher and silently drops a failing voucher),
// and must never be settled against B's order.
func TestIntegrationVoucherOfOtherTenantNotRedeemable(t *testing.T) {
	e := newIsolationITest(t)

	// The owner of A mints a 100% voucher through the admin API.
	code := "FREE-" + randSuffix()
	rec := e.do(http.MethodPost, "/api/v1/admin/vouchers", e.owner, e.tenA, `{"code":"`+code+`","kind":"percent","percentBps":10000}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	e.voucher[code] = e.tenA

	productB := e.scalar(`INSERT INTO products(title,slug,price,tenant_id) VALUES('Iso product',$1,50000,$2) RETURNING id::text`, "iso-prod-"+randSuffix(), e.tenB)
	cartB := e.scalar(`INSERT INTO carts(user_id,tenant_id) VALUES($1,$2) RETURNING id::text`, e.buyer, e.tenB)
	e.exec(`INSERT INTO cart_items(cart_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'Iso product','iso',1,50000,50000)`, cartB, productB)

	tenB, err := cart.ToUUID(e.tenB)
	require.NoError(t, err)
	cartSvc := &cart.Service{Q: e.q, Pool: e.pool, DefaultTenantID: tenB}
	cartHandler := &cart.Handler{Q: e.q, Svc: cartSvc}
	r := chi.NewRouter()
	r.Post("/carts/{id}/apply-voucher", cartHandler.ApplyVoucher)

	req := httptest.NewRequest(http.MethodPost, "/carts/"+cartB+"/apply-voucher", strings.NewReader(`{"code":"`+code+`"}`))
	req = req.WithContext(tenant.WithTenant(common.WithUserID(req.Context(), e.buyer), e.tenB))
	applyRec := httptest.NewRecorder()
	r.ServeHTTP(applyRec, req)
	assert.Equal(t, http.StatusNotFound, applyRec.Code, applyRec.Body.String())
	assert.Equal(t, "", e.scalar(`SELECT COALESCE(applied_voucher_code,'') FROM carts WHERE id=$1`, cartB))

	// Checkout path: even if the code were stored on the cart, pricing rejects it.
	e.exec(`UPDATE carts SET applied_voucher_code=$1 WHERE id=$2`, code, cartB)
	cartUUID, err := cart.ToUUID(cartB)
	require.NoError(t, err)
	ctxB := tenant.WithTenant(context.Background(), e.tenB)
	discount, _, err := cartSvc.EvaluateVoucher(ctxB, cartUUID, code)
	assert.Error(t, err)
	assert.Zero(t, discount)

	// Control: the same code is redeemable in its own tenant.
	productA := e.scalar(`INSERT INTO products(title,slug,price,tenant_id) VALUES('Iso product',$1,50000,$2) RETURNING id::text`, "iso-prod-"+randSuffix(), e.tenA)
	cartA := e.scalar(`INSERT INTO carts(user_id,tenant_id) VALUES($1,$2) RETURNING id::text`, e.buyer, e.tenA)
	e.exec(`INSERT INTO cart_items(cart_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'Iso product','iso',1,50000,50000)`, cartA, productA)
	cartAUUID, err := cart.ToUUID(cartA)
	require.NoError(t, err)
	discount, _, err = cartSvc.EvaluateVoucher(tenant.WithTenant(context.Background(), e.tenA), cartAUUID, code)
	require.NoError(t, err)
	assert.Equal(t, int64(50000), discount)

	// Settlement against B's order ignores A's voucher.
	vSvc := &voucher.Service{Q: e.q}
	orderB, err := cart.ToUUID(e.orderB)
	require.NoError(t, err)
	buyer, err := cart.ToUUID(e.buyer)
	require.NoError(t, err)
	require.NoError(t, vSvc.Settle(context.Background(), pgtype.UUID(tenB), code, orderB, buyer, 50000))
	assert.Equal(t, "0", e.scalar(`SELECT used_count::text FROM vouchers WHERE code=$1`, code))
}
