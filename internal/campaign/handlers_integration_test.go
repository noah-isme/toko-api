package campaign_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/campaign"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// These tests drive the admin flash-sale handlers against a real Postgres with
// all migrations applied. They are opt-in: set TEST_DATABASE_URL to run them.

const productPrice = int64(100000)

type itest struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	tenA   string
	tenB   string
	// products[0..3] belong to tenant A; prodB belongs to tenant B.
	products []string
	prodB    string
}

func randSlug(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func newITest(t *testing.T) *itest {
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

	e := &itest{t: t, pool: pool}
	e.tenA = e.insertTenant()
	e.tenB = e.insertTenant()
	// C2: orders first (they cascade to flash_sale_order_items), then campaigns
	// (cascade to items), then products, then tenants. Registered after
	// pool.Close so it runs before it.
	t.Cleanup(func() {
		cctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM flash_sale_campaigns WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM tenants WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(cctx, stmt, []string{e.tenA, e.tenB}); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	for i := 0; i < 4; i++ {
		e.products = append(e.products, e.insertProduct(e.tenA))
	}
	e.prodB = e.insertProduct(e.tenB)

	h := &campaign.Handler{Pool: pool}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if id := req.Header.Get("X-Tenant-ID"); id != "" {
				req = req.WithContext(tenant.WithTenant(req.Context(), id))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/admin/flash-sales", h.AdminList)
	r.Post("/admin/flash-sales", h.AdminCreate)
	r.Get("/admin/flash-sales/{id}", h.AdminGet)
	r.Patch("/admin/flash-sales/{id}", h.AdminUpdate)
	e.router = r
	return e
}

func (e *itest) insertTenant() string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(context.Background(), `INSERT INTO tenants(slug,name) VALUES($1,$2) RETURNING id::text`, randSlug("it-tenant"), "IT tenant").Scan(&id)
	require.NoError(e.t, err)
	return id
}

func (e *itest) insertProduct(tenantID string) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(context.Background(), `INSERT INTO products(title,slug,price,tenant_id) VALUES($1,$2,$3,$4) RETURNING id::text`, "IT product", randSlug("it-prod"), productPrice, tenantID).Scan(&id)
	require.NoError(e.t, err)
	return id
}

type seededItem struct {
	ID        string
	ProductID string
}

// seedCampaign inserts an ACTIVE campaign for the tenant directly with SQL and
// one item per product (sale price 50000, no stock limit).
func (e *itest) seedCampaign(tenantID string, productIDs ...string) (string, []seededItem) {
	e.t.Helper()
	ctx := context.Background()
	now := time.Now()
	var id string
	err := e.pool.QueryRow(ctx, `INSERT INTO flash_sale_campaigns(tenant_id,name,slug,status,starts_at,ends_at) VALUES($1,$2,$3,'ACTIVE',$4,$5) RETURNING id::text`,
		tenantID, "IT campaign", randSlug("it-camp"), now.Add(-time.Hour), now.Add(48*time.Hour)).Scan(&id)
	require.NoError(e.t, err)
	var items []seededItem
	for _, p := range productIDs {
		var itemID string
		err := e.pool.QueryRow(ctx, `INSERT INTO flash_sale_items(campaign_id,product_id,sale_price) VALUES($1,$2,50000) RETURNING id::text`, id, p).Scan(&itemID)
		require.NoError(e.t, err)
		items = append(items, seededItem{ID: itemID, ProductID: p})
	}
	return id, items
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

func (r resp) items() []map[string]any {
	var out []map[string]any
	raw, _ := r.data()["items"].([]any)
	for _, it := range raw {
		out = append(out, it.(map[string]any))
	}
	return out
}

func (e *itest) do(method, path, tenantID string, body any) resp {
	e.t.Helper()
	var reader *bytes.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		require.NoError(e.t, err)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if tenantID != "" {
		req.Header.Set("X-Tenant-ID", tenantID)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	out := resp{Status: rec.Code, Raw: rec.Body.String()}
	require.NoError(e.t, json.Unmarshal(rec.Body.Bytes(), &out.Body), "response is not JSON: %s", out.Raw)
	// No response may leak a raw Postgres message.
	for _, leak := range []string{"SQLSTATE", "violates", "duplicate key", "pgx", "ERROR:"} {
		require.NotContains(e.t, out.Raw, leak, "response leaks a database message")
	}
	return out
}

func (e *itest) patch(campaignID, tenantID string, body any) resp {
	e.t.Helper()
	return e.do(http.MethodPatch, "/admin/flash-sales/"+campaignID, tenantID, body)
}

func (e *itest) get(campaignID, tenantID string) resp {
	e.t.Helper()
	return e.do(http.MethodGet, "/admin/flash-sales/"+campaignID, tenantID, nil)
}

func (e *itest) itemCount(campaignID string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM flash_sale_items WHERE campaign_id=$1`, campaignID).Scan(&n))
	return n
}

func (e *itest) itemExists(itemID string) bool {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM flash_sale_items WHERE id=$1`, itemID).Scan(&n))
	return n > 0
}

func (e *itest) itemProduct(itemID string) string {
	e.t.Helper()
	var p string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT product_id::text FROM flash_sale_items WHERE id=$1`, itemID).Scan(&p))
	return p
}

func (e *itest) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

// reservation inserts a minimal order plus a flash_sale_order_items row.
func (e *itest) reservation(tenantID, itemID, status string) string {
	e.t.Helper()
	ctx := context.Background()
	var orderID string
	require.NoError(e.t, e.pool.QueryRow(ctx, `INSERT INTO orders(pricing_subtotal,pricing_total,tenant_id) VALUES(1,1,$1) RETURNING id::text`, tenantID).Scan(&orderID))
	var rowID string
	require.NoError(e.t, e.pool.QueryRow(ctx, `INSERT INTO flash_sale_order_items(order_id,flash_sale_item_id,qty,status) VALUES($1,$2,1,$3) RETURNING id::text`, orderID, itemID, status).Scan(&rowID))
	return rowID
}

func (e *itest) reservationExists(rowID string) bool {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM flash_sale_order_items WHERE id=$1`, rowID).Scan(&n))
	return n > 0
}

func rfc(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339) }

func itemBody(id *string, productID string, salePrice int64, stockLimit any) map[string]any {
	m := map[string]any{"productId": productID, "salePrice": salePrice, "stockLimit": stockLimit}
	if id != nil {
		m["id"] = *id
	}
	return m
}

func createBody(slug string, items ...map[string]any) map[string]any {
	return map[string]any{
		"name":     "IT campaign",
		"slug":     slug,
		"status":   "DRAFT",
		"startsAt": rfc(time.Hour),
		"endsAt":   rfc(48 * time.Hour),
		"items":    items,
	}
}

// Case group 1: create.
func TestIntegrationAdminCreate(t *testing.T) {
	e := newITest(t)

	t.Run("RFC3339 dates create the campaign", func(t *testing.T) {
		r := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, createBody(randSlug("c"),
			itemBody(nil, e.products[0], 50000, nil), itemBody(nil, e.products[1], 40000, 10)))
		require.Equal(t, http.StatusCreated, r.Status, r.Raw)
		id, _ := r.data()["id"].(string)
		require.NotEmpty(t, id)
		got := e.get(id, e.tenA)
		require.Equal(t, http.StatusOK, got.Status, got.Raw)
		require.Len(t, got.items(), 2)
	})

	t.Run("zone-less startsAt is rejected with the RFC 3339 message", func(t *testing.T) {
		body := createBody(randSlug("c"), itemBody(nil, e.products[0], 50000, nil))
		body["startsAt"] = "2026-12-01T00:00"
		r := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, body)
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Contains(t, r.errMessage(), "RFC 3339")
	})

	t.Run("product of another tenant is rejected", func(t *testing.T) {
		r := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, createBody(randSlug("c"), itemBody(nil, e.prodB, 50000, nil)))
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.NotContains(t, strings.ToLower(r.Raw), "another tenant")
	})

	t.Run("salePrice not below product price is rejected", func(t *testing.T) {
		for _, price := range []int64{productPrice, productPrice + 1} {
			r := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, createBody(randSlug("c"), itemBody(nil, e.products[0], price, nil)))
			require.Equal(t, http.StatusBadRequest, r.Status, "salePrice=%d: %s", price, r.Raw)
			require.Contains(t, r.errMessage(), "salePrice")
		}
	})

	t.Run("duplicate productId is rejected", func(t *testing.T) {
		r := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, createBody(randSlug("c"),
			itemBody(nil, e.products[0], 50000, nil), itemBody(nil, e.products[0], 40000, nil)))
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Contains(t, r.errMessage(), "duplicate")
	})

	t.Run("duplicate slug in a tenant returns 409 without a raw database message", func(t *testing.T) {
		slug := randSlug("c")
		first := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, createBody(slug, itemBody(nil, e.products[0], 50000, nil)))
		require.Equal(t, http.StatusCreated, first.Status, first.Raw)
		second := e.do(http.MethodPost, "/admin/flash-sales", e.tenA, createBody(slug, itemBody(nil, e.products[0], 50000, nil)))
		require.Equal(t, http.StatusConflict, second.Status, second.Raw)
		require.Equal(t, "CONFLICT", second.errCode())
	})
}

// Case group 2: status-only compatibility and strict inputs.
func TestIntegrationAdminUpdateStatusOnly(t *testing.T) {
	e := newITest(t)
	id, _ := e.seedCampaign(e.tenA, e.products[0], e.products[1])

	t.Run("status-only body updates status and returns the full campaign", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"status": "SCHEDULED"})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		require.Equal(t, id, r.data()["id"])
		require.Equal(t, "SCHEDULED", r.data()["status"])
		require.NotEmpty(t, r.data()["updatedAt"])
		require.Len(t, r.items(), 2)
		require.Equal(t, 2, e.itemCount(id))
		r = e.patch(id, e.tenA, map[string]any{"status": "ACTIVE"})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		require.Equal(t, "ACTIVE", r.data()["status"])
		require.Len(t, r.items(), 2)
	})

	t.Run("empty status is rejected, not coerced to DRAFT", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"status": ""})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Equal(t, "ACTIVE", e.get(id, e.tenA).data()["status"])
	})

	t.Run("unknown status is rejected", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"status": "PAUSED"})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
	})

	t.Run("zone-less startsAt on PATCH is rejected", func(t *testing.T) {
		r := e.patch(id, e.tenA, `{"startsAt":"2026-12-01T00:00"}`)
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Contains(t, r.errMessage(), "RFC 3339")
	})
}

// Case group 3: full body with update, add and remove.
func TestIntegrationAdminUpdateStockLimitAbsentKeepsValue(t *testing.T) {
	e := newITest(t)
	id, items := e.seedCampaign(e.tenA, e.products[0])
	itemA := items[0]

	findItem := func(r resp) map[string]any {
		t.Helper()
		for _, it := range r.items() {
			if it["id"] == itemA.ID {
				return it
			}
		}
		t.Fatalf("item %s missing from %s", itemA.ID, r.Raw)
		return nil
	}
	patchItems := func(item map[string]any) {
		t.Helper()
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{item}})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
	}

	patchItems(itemBody(&itemA.ID, e.products[0], 30000, 7))
	require.EqualValues(t, 7, findItem(e.get(id, e.tenA))["stockLimit"])

	omitted := itemBody(&itemA.ID, e.products[0], 31000, nil)
	delete(omitted, "stockLimit")
	patchItems(omitted)
	got := findItem(e.get(id, e.tenA))
	require.EqualValues(t, 7, got["stockLimit"], "absent stockLimit must keep the current limit")
	require.EqualValues(t, 31000, got["salePrice"])

	patchItems(itemBody(&itemA.ID, e.products[0], 31000, nil))
	got = findItem(e.get(id, e.tenA))
	require.NotContains(t, got, "stockLimit", "explicit null must clear the limit")
}

func TestIntegrationAdminUpdateFullBody(t *testing.T) {
	e := newITest(t)
	id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
	itemA, itemC := items[0], items[1]
	newSlug := randSlug("renamed")

	r := e.patch(id, e.tenA, map[string]any{
		"name":     "Renamed",
		"slug":     newSlug,
		"status":   "SCHEDULED",
		"startsAt": rfc(2 * time.Hour),
		"endsAt":   rfc(72 * time.Hour),
		"items": []map[string]any{
			itemBody(&itemA.ID, e.products[0], 30000, 7),
			itemBody(nil, e.products[2], 20000, nil),
		},
	})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	require.Equal(t, "Renamed", r.data()["name"])
	require.Equal(t, newSlug, r.data()["slug"])
	require.Equal(t, "SCHEDULED", r.data()["status"])

	got := r.items()
	require.Len(t, got, 2)
	byProduct := map[string]map[string]any{}
	for _, it := range got {
		byProduct[it["productId"].(string)] = it
	}
	require.Contains(t, byProduct, e.products[0])
	require.Contains(t, byProduct, e.products[2])
	require.NotContains(t, byProduct, e.products[1])
	require.Equal(t, itemA.ID, byProduct[e.products[0]]["id"])
	require.EqualValues(t, 30000, byProduct[e.products[0]]["salePrice"])
	require.EqualValues(t, 7, byProduct[e.products[0]]["stockLimit"])
	require.False(t, e.itemExists(itemC.ID), "omitted item must be deleted")
	require.Equal(t, 2, e.itemCount(id))

	// A subsequent GET returns the same state.
	again := e.get(id, e.tenA)
	require.Equal(t, http.StatusOK, again.Status, again.Raw)
	require.Len(t, again.items(), 2)
}

// Case group 4: items with sales.
func TestIntegrationAdminUpdateItemsWithSales(t *testing.T) {
	e := newITest(t)
	id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
	itemA, itemD := items[0], items[1]
	e.exec(`UPDATE flash_sale_items SET sold_count=2, stock_limit=5 WHERE id=$1`, itemA.ID)

	t.Run("omitting an item with sales returns 409", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{itemBody(&itemD.ID, e.products[1], 50000, nil)}})
		require.Equal(t, http.StatusConflict, r.Status, r.Raw)
		require.Equal(t, "CONFLICT", r.errCode())
		require.True(t, e.itemExists(itemA.ID))
	})

	t.Run("stockLimit below soldCount returns 400", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&itemA.ID, e.products[0], 50000, 1),
			itemBody(&itemD.ID, e.products[1], 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
	})

	t.Run("changing the product of an item with sales returns 400", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&itemA.ID, e.products[2], 50000, 5),
			itemBody(&itemD.ID, e.products[1], 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Equal(t, e.products[0], e.itemProduct(itemA.ID))
	})

	t.Run("lowering salePrice of an item with sales succeeds", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&itemA.ID, e.products[0], 40000, 5),
			itemBody(&itemD.ID, e.products[1], 50000, nil),
		}})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		var price int64
		require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT sale_price FROM flash_sale_items WHERE id=$1`, itemA.ID).Scan(&price))
		require.EqualValues(t, 40000, price)
		var sold int
		require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT sold_count FROM flash_sale_items WHERE id=$1`, itemA.ID).Scan(&sold))
		require.Equal(t, 2, sold)
	})
}

// Case group 5 (D5): removing items that carry reservation rows.
func TestIntegrationAdminUpdateReleasedReservations(t *testing.T) {
	e := newITest(t)

	t.Run("RELEASED rows are deleted together with the removed item", func(t *testing.T) {
		id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
		keep, drop := items[0], items[1]
		row := e.reservation(e.tenA, drop.ID, "RELEASED")
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{itemBody(&keep.ID, e.products[0], 50000, nil)}})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		require.Len(t, r.items(), 1)
		require.False(t, e.itemExists(drop.ID))
		require.False(t, e.reservationExists(row))
	})

	t.Run("RESERVED rows with sold_count 1 block removal with 409", func(t *testing.T) {
		id, items := e.seedCampaign(e.tenA, e.products[2], e.products[3])
		keep, drop := items[0], items[1]
		e.exec(`UPDATE flash_sale_items SET sold_count=1 WHERE id=$1`, drop.ID)
		row := e.reservation(e.tenA, drop.ID, "RESERVED")
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{itemBody(&keep.ID, e.products[2], 50000, nil)}})
		require.Equal(t, http.StatusConflict, r.Status, r.Raw)
		require.True(t, e.itemExists(drop.ID))
		require.True(t, e.reservationExists(row))
	})

	t.Run("RESERVED row with sold_count 0 hits the FK fallback as 409", func(t *testing.T) {
		// Inconsistent data (should not occur): proves the FK is mapped to 409
		// and that a RESERVED row is never deleted by the RELEASED cleanup.
		id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
		keep, drop := items[0], items[1]
		row := e.reservation(e.tenA, drop.ID, "RESERVED")
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{itemBody(&keep.ID, e.products[0], 50000, nil)}})
		require.Equal(t, http.StatusConflict, r.Status, r.Raw)
		require.Equal(t, "item has active reservations", r.errMessage())
		require.True(t, e.itemExists(drop.ID))
		require.True(t, e.reservationExists(row))
	})
}

// Case group 6: slug uniqueness is per tenant.
func TestIntegrationAdminUpdateSlugConflict(t *testing.T) {
	e := newITest(t)
	idX, _ := e.seedCampaign(e.tenA, e.products[0])
	idY, _ := e.seedCampaign(e.tenA, e.products[1])
	idB, _ := e.seedCampaign(e.tenB, e.prodB)
	var slugX string
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT slug FROM flash_sale_campaigns WHERE id=$1`, idX).Scan(&slugX))

	t.Run("slug of another campaign in the same tenant returns 409", func(t *testing.T) {
		r := e.patch(idY, e.tenA, map[string]any{"slug": slugX})
		require.Equal(t, http.StatusConflict, r.Status, r.Raw)
		require.Equal(t, "CONFLICT", r.errCode())
		require.Equal(t, "campaign slug already exists", r.errMessage())
	})

	t.Run("same slug in another tenant succeeds", func(t *testing.T) {
		r := e.patch(idB, e.tenB, map[string]any{"slug": slugX})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		require.Equal(t, slugX, r.data()["slug"])
	})

	t.Run("a rejected request leaves the campaign untouched", func(t *testing.T) {
		id, items := e.seedCampaign(e.tenA, e.products[2], e.products[3])
		// Both items target the same product; the request is rejected before any
		// write, so the rename must not be applied.
		r := e.patch(id, e.tenA, map[string]any{"name": "should roll back", "items": []map[string]any{
			itemBody(&items[0].ID, e.products[3], 50000, nil),
			itemBody(&items[1].ID, e.products[3], 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw) // duplicate productId in request
		require.Equal(t, "IT campaign", e.get(id, e.tenA).data()["name"])
	})
}

// Case group 7: tenant isolation.
func TestIntegrationAdminUpdateTenantIsolation(t *testing.T) {
	e := newITest(t)
	id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
	idB, _ := e.seedCampaign(e.tenB, e.prodB)

	t.Run("updating an item to a product of another tenant returns 400", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&items[0].ID, e.prodB, 50000, nil),
			itemBody(&items[1].ID, e.products[1], 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Equal(t, e.products[0], e.itemProduct(items[0].ID))
	})

	t.Run("adding a product of another tenant returns 400", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&items[0].ID, e.products[0], 50000, nil),
			itemBody(nil, e.prodB, 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Equal(t, 2, e.itemCount(id))
	})

	t.Run("a campaign owned by another tenant returns 404", func(t *testing.T) {
		r := e.patch(idB, e.tenA, map[string]any{"status": "ENDED"})
		require.Equal(t, http.StatusNotFound, r.Status, r.Raw)
		require.Equal(t, "NOT_FOUND", r.errCode())
		var status string
		require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT status FROM flash_sale_campaigns WHERE id=$1`, idB).Scan(&status))
		require.Equal(t, "ACTIVE", status)
		require.Equal(t, http.StatusNotFound, e.get(idB, e.tenA).Status)
	})

	t.Run("items of another campaign are rejected", func(t *testing.T) {
		_, otherItems := e.seedCampaign(e.tenA, e.products[2])
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&otherItems[0].ID, e.products[2], 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
	})
}

// Case group 8: validation and product moves.
func TestIntegrationAdminUpdateValidation(t *testing.T) {
	e := newITest(t)

	t.Run("empty items and empty body are rejected", func(t *testing.T) {
		id, _ := e.seedCampaign(e.tenA, e.products[0])
		r := e.patch(id, e.tenA, `{"items":[]}`)
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Equal(t, 1, e.itemCount(id))
		r = e.patch(id, e.tenA, `{}`)
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
	})

	t.Run("endsAt not after startsAt is rejected", func(t *testing.T) {
		id, _ := e.seedCampaign(e.tenA, e.products[0])
		same := rfc(5 * time.Hour)
		r := e.patch(id, e.tenA, map[string]any{"startsAt": same, "endsAt": same})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		r = e.patch(id, e.tenA, map[string]any{"startsAt": rfc(5 * time.Hour), "endsAt": rfc(4 * time.Hour)})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
	})

	t.Run("active campaign with endsAt in the past is rejected", func(t *testing.T) {
		id, _ := e.seedCampaign(e.tenA, e.products[0])
		r := e.patch(id, e.tenA, map[string]any{"startsAt": rfc(-3 * time.Hour), "endsAt": rfc(-2 * time.Hour)})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		// ENDED campaigns can still be rescheduled into the past.
		r = e.patch(id, e.tenA, map[string]any{"status": "ENDED", "startsAt": rfc(-3 * time.Hour), "endsAt": rfc(-2 * time.Hour)})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
	})

	t.Run("swapping the products of two items is rejected", func(t *testing.T) {
		id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{
			itemBody(&items[0].ID, e.products[1], 50000, nil),
			itemBody(&items[1].ID, e.products[0], 50000, nil),
		}})
		require.Equal(t, http.StatusBadRequest, r.Status, r.Raw)
		require.Contains(t, r.errMessage(), "swapping")
		require.Equal(t, e.products[0], e.itemProduct(items[0].ID))
		require.Equal(t, e.products[1], e.itemProduct(items[1].ID))
	})

	t.Run("a product-move chain succeeds in either request order", func(t *testing.T) {
		for _, reversed := range []bool{false, true} {
			id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1], e.products[2])
			// A: p0->p1, B: p1->p2, C: p2->p3
			moves := []map[string]any{
				itemBody(&items[0].ID, e.products[1], 50000, nil),
				itemBody(&items[1].ID, e.products[2], 50000, nil),
				itemBody(&items[2].ID, e.products[3], 50000, nil),
			}
			if reversed {
				moves[0], moves[2] = moves[2], moves[0]
			}
			r := e.patch(id, e.tenA, map[string]any{"items": moves})
			require.Equal(t, http.StatusOK, r.Status, "reversed=%v: %s", reversed, r.Raw)
			require.Equal(t, e.products[1], e.itemProduct(items[0].ID))
			require.Equal(t, e.products[2], e.itemProduct(items[1].ID))
			require.Equal(t, e.products[3], e.itemProduct(items[2].ID))
			require.Len(t, r.items(), 3)
		}
	})

	t.Run("a move onto the product of a removed item succeeds", func(t *testing.T) {
		id, items := e.seedCampaign(e.tenA, e.products[0], e.products[1])
		r := e.patch(id, e.tenA, map[string]any{"items": []map[string]any{itemBody(&items[0].ID, e.products[1], 50000, nil)}})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		require.False(t, e.itemExists(items[1].ID))
		require.Equal(t, e.products[1], e.itemProduct(items[0].ID))
	})
}

// Case group 9: campaigns whose items vanished with their product.
func TestIntegrationAdminItemlessCampaign(t *testing.T) {
	e := newITest(t)
	id, _ := e.seedCampaign(e.tenA, e.products[0])
	e.exec(`DELETE FROM products WHERE id=$1`, e.products[0])
	require.Equal(t, 0, e.itemCount(id), "product delete should cascade the item")

	t.Run("GET returns the campaign with empty items", func(t *testing.T) {
		r := e.get(id, e.tenA)
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		raw, ok := r.data()["items"].([]any)
		require.True(t, ok, "items must be an array, not null: %s", r.Raw)
		require.Empty(t, raw)
	})

	t.Run("list returns 200 including the item-less campaign", func(t *testing.T) {
		r := e.do(http.MethodGet, "/admin/flash-sales", e.tenA, nil)
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		list, _ := r.Body["data"].([]any)
		require.Len(t, list, 1)
		first := list[0].(map[string]any)
		require.Equal(t, id, first["id"])
		items, ok := first["items"].([]any)
		require.True(t, ok)
		require.Empty(t, items)
	})

	t.Run("PATCH status ENDED succeeds", func(t *testing.T) {
		r := e.patch(id, e.tenA, map[string]any{"status": "ENDED"})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
		require.Equal(t, "ENDED", r.data()["status"])
		require.Empty(t, r.items())
	})
}
