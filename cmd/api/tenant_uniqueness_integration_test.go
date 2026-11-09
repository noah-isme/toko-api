package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/tenant"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

// Catalog and voucher identifiers are unique per tenant (migrations
// 000057-000070): two tenants may use the same product slug, category slug,
// brand name/slug, variant SKU and voucher code, while a duplicate inside one
// tenant keeps answering 409 CONFLICT. A 409 never carries another tenant's
// data. Opt-in: set TEST_DATABASE_URL.
func TestIntegrationCatalogUniquenessIsPerTenant(t *testing.T) {
	e := newIsolationITest(t)
	// The same user owns both tenants, so every request below passes the
	// membership and permission guards and only the uniqueness rule differs.
	e.exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'OWNER','ACTIVE')`, e.tenB, e.owner)

	sfx := randSuffix()
	shared := "shared-" + sfx
	bOnly := "b-only-" + sfx
	sku := "SKU-" + strings.ToUpper(sfx)
	code := "SAME-" + strings.ToUpper(sfx)

	// conflictIsClean asserts a within-tenant duplicate in A: 409 CONFLICT,
	// and nothing in the body that only tenant B has.
	conflictIsClean := func(t *testing.T, rec interface {
		Result() *http.Response
	}, body string) {
		t.Helper()
		assert.Equal(t, http.StatusConflict, rec.Result().StatusCode, body)
		var parsed struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &parsed), body)
		assert.Equal(t, "CONFLICT", parsed.Error.Code, body)
		assert.NotContains(t, body, e.tenB)
		assert.NotContains(t, body, bOnly)
		assert.NotContains(t, body, strings.ToUpper(bOnly))
	}

	t.Run("product slug and variant sku", func(t *testing.T) {
		product := func(tenantID, slug, variantSKU string) (int, string) {
			rec := e.do(http.MethodPost, "/api/v1/admin/products", e.owner, tenantID,
				`{"title":"Same product","price":1000,"slug":"`+slug+`","variants":[{"sku":"`+variantSKU+`","price":1000,"stock":1}]}`)
			return rec.Code, rec.Body.String()
		}
		status, body := product(e.tenB, bOnly, strings.ToUpper(bOnly))
		require.Equal(t, http.StatusCreated, status, body)

		status, body = product(e.tenA, shared, sku)
		require.Equal(t, http.StatusCreated, status, body)
		status, body = product(e.tenB, shared, sku)
		require.Equal(t, http.StatusCreated, status, body, "tenant B may reuse tenant A's slug and SKU")
		assert.Equal(t, "2", e.scalar(`SELECT COUNT(*)::text FROM products WHERE slug=$1 AND tenant_id = ANY($2::uuid[])`, shared, []string{e.tenA, e.tenB}))
		assert.Equal(t, "2", e.scalar(`SELECT COUNT(*)::text FROM product_variants WHERE sku=$1 AND tenant_id = ANY($2::uuid[])`, sku, []string{e.tenA, e.tenB}))
		// product_variants.tenant_id is derived from the product.
		assert.Equal(t, "0", e.scalar(`SELECT COUNT(*)::text FROM product_variants v JOIN products p ON p.id=v.product_id WHERE v.sku=$1 AND v.tenant_id <> p.tenant_id`, sku))

		// Duplicate slug inside A.
		rec := e.do(http.MethodPost, "/api/v1/admin/products", e.owner, e.tenA, `{"title":"Again","price":1000,"slug":"`+shared+`"}`)
		conflictIsClean(t, rec, rec.Body.String())

		// Duplicate SKU inside A (new product, free slug).
		rec = e.do(http.MethodPost, "/api/v1/admin/products", e.owner, e.tenA,
			`{"title":"Other","price":1000,"slug":"other-`+sfx+`","variants":[{"sku":"`+sku+`","price":1000,"stock":1}]}`)
		conflictIsClean(t, rec, rec.Body.String())
		assert.Equal(t, "1", e.scalar(`SELECT COUNT(*)::text FROM product_variants WHERE sku=$1 AND tenant_id=$2`, sku, e.tenA))
	})

	t.Run("category slug", func(t *testing.T) {
		create := func(tenantID, slug string) (int, string) {
			rec := e.do(http.MethodPost, "/api/v1/admin/categories", e.owner, tenantID, `{"name":"Same category","slug":"`+slug+`"}`)
			return rec.Code, rec.Body.String()
		}
		status, body := create(e.tenB, bOnly)
		require.Equal(t, http.StatusCreated, status, body)
		status, body = create(e.tenA, shared)
		require.Equal(t, http.StatusCreated, status, body)
		status, body = create(e.tenB, shared)
		require.Equal(t, http.StatusCreated, status, body, "tenant B may reuse tenant A's category slug")

		rec := e.do(http.MethodPost, "/api/v1/admin/categories", e.owner, e.tenA, `{"name":"Again","slug":"`+shared+`"}`)
		conflictIsClean(t, rec, rec.Body.String())
	})

	t.Run("brand name and slug", func(t *testing.T) {
		name := "Same Brand " + sfx
		rec := e.do(http.MethodPost, "/api/v1/admin/brands", e.owner, e.tenB, `{"name":"`+bOnly+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = e.do(http.MethodPost, "/api/v1/admin/brands", e.owner, e.tenA, `{"name":"`+name+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = e.do(http.MethodPost, "/api/v1/admin/brands", e.owner, e.tenB, `{"name":"`+name+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code, "tenant B may reuse tenant A's brand name and slug: %s", rec.Body.String())
		assert.Equal(t, "2", e.scalar(`SELECT COUNT(*)::text FROM brands WHERE name=$1 AND slug=$2`, name, "same-brand-"+sfx))

		// Same name inside A, and same slug under another name inside A.
		rec = e.do(http.MethodPost, "/api/v1/admin/brands", e.owner, e.tenA, `{"name":"`+name+`","slug":"different-`+sfx+`"}`)
		conflictIsClean(t, rec, rec.Body.String())
		rec = e.do(http.MethodPost, "/api/v1/admin/brands", e.owner, e.tenA, `{"name":"Another `+sfx+`","slug":"same-brand-`+sfx+`"}`)
		conflictIsClean(t, rec, rec.Body.String())
	})

	t.Run("voucher code", func(t *testing.T) {
		rec := e.do(http.MethodPost, "/api/v1/admin/vouchers", e.owner, e.tenB, `{"code":"`+strings.ToUpper(bOnly)+`","value":100,"kind":"fixed_amount"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		e.voucher[strings.ToUpper(bOnly)] = e.tenB

		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers", e.owner, e.tenA, `{"code":"`+code+`","value":1000,"kind":"fixed_amount"}`)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers", e.owner, e.tenB, `{"code":"`+code+`","value":3000,"kind":"fixed_amount"}`)
		require.Equal(t, http.StatusCreated, rec.Code, "tenant B may reuse tenant A's voucher code: %s", rec.Body.String())

		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers", e.owner, e.tenA, `{"code":"`+code+`","value":5,"kind":"fixed_amount"}`)
		conflictIsClean(t, rec, rec.Body.String())

		// Every lookup by code stays inside the tenant: update, preview,
		// cart pricing and settlement each see their own tenant's voucher.
		rec = e.do(http.MethodPut, "/api/v1/admin/vouchers/"+code, e.owner, e.tenA, `{"code":"`+code+`","value":1500,"kind":"fixed_amount"}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "1500", e.scalar(`SELECT value::text FROM vouchers WHERE code=$1 AND tenant_id=$2`, code, e.tenA))
		assert.Equal(t, "3000", e.scalar(`SELECT value::text FROM vouchers WHERE code=$1 AND tenant_id=$2`, code, e.tenB))

		rec = e.do(http.MethodPost, "/api/v1/admin/vouchers/preview", e.owner, e.tenB, `{"code":"`+code+`","cartTotal":10000,"items":[{"subtotal":10000}]}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), `"discount":3000`)

		discountIn := func(tenantID string) int64 {
			productID := e.scalar(`INSERT INTO products(title,slug,price,tenant_id) VALUES('Iso product',$1,50000,$2) RETURNING id::text`, "iso-prod-"+randSuffix(), tenantID)
			cartID := e.scalar(`INSERT INTO carts(user_id,tenant_id) VALUES($1,$2) RETURNING id::text`, e.buyer, tenantID)
			e.exec(`INSERT INTO cart_items(cart_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'Iso product','iso',1,50000,50000)`, cartID, productID)
			cartUUID, err := cart.ToUUID(cartID)
			require.NoError(t, err)
			svc := &cart.Service{Q: e.q, Pool: e.pool}
			discount, _, err := svc.EvaluateVoucher(tenant.WithTenant(context.Background(), tenantID), cartUUID, code)
			require.NoError(t, err)
			return discount
		}
		assert.Equal(t, int64(1500), discountIn(e.tenA))
		assert.Equal(t, int64(3000), discountIn(e.tenB))

		orderA, err := cart.ToUUID(e.orderA)
		require.NoError(t, err)
		tenA, err := cart.ToUUID(e.tenA)
		require.NoError(t, err)
		require.NoError(t, (&voucher.Service{Q: e.q}).Settle(context.Background(), tenA, code, orderA, pgtype.UUID{}, 1500))
		assert.Equal(t, "1", e.scalar(`SELECT used_count::text FROM vouchers WHERE code=$1 AND tenant_id=$2`, code, e.tenA))
		assert.Equal(t, "0", e.scalar(`SELECT used_count::text FROM vouchers WHERE code=$1 AND tenant_id=$2`, code, e.tenB))
	})
}
