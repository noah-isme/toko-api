package catalog_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/catalog"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Postgres integration tests for search, facets, suggestions and the popularity
// counter. Opt-in: set TEST_DATABASE_URL to a database with all migrations applied.

type searchFixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	svc  *catalog.Service
	tenA string
	tenB string
	// product ids/slugs by short key: sepatu, kaos, kemeja (tenant A), sandal (tenant B).
	ids   map[string]string
	slugs map[string]string
	// brand/category slugs by short key (slugs and names are globally unique in the schema).
	terms map[string]string
	users []string
}

func randSuffix() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newSearchFixture(t *testing.T) *searchFixture {
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

	f := &searchFixture{t: t, pool: pool, ids: map[string]string{}, slugs: map[string]string{}, terms: map[string]string{}}
	f.tenA = f.insertTenant()
	f.tenB = f.insertTenant()
	t.Cleanup(func() {
		cctx := context.Background()
		tenants := []string{f.tenA, f.tenB}
		for _, stmt := range []string{
			`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM brands WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM categories WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM tenants WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(cctx, stmt, tenants); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if len(f.users) > 0 {
			if _, err := pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, f.users); err != nil {
				t.Errorf("cleanup users: %v", err)
			}
		}
	})

	nike := f.insertNamed("brands", f.tenA, "Nike", "nike")
	acme := f.insertNamed("brands", f.tenA, "Acme", "acme")
	sepatuCat := f.insertNamed("categories", f.tenA, "Sepatu", "sepatu")
	pakaian := f.insertNamed("categories", f.tenA, "Pakaian", "pakaian")
	f.insertProduct("sepatu", f.tenA, "Sepatu Lari Nike Pegasus", "Sepatu untuk lari jarak jauh", nike, sepatuCat, 1_500_000, true)
	f.insertProduct("kaos", f.tenA, "Kaos Hitam Polos", "Kaos katun", acme, pakaian, 75_000, true)
	f.insertProduct("kemeja", f.tenA, "Kemeja Batik Lengan Panjang", "Kemeja batik tulis", acme, pakaian, 250_000, false)
	f.insertProduct("sandal", f.tenB, "Sepatu Sandal Jepit", "Sandal", nil, nil, 50_000, true)

	svc, err := catalog.NewService(catalog.ServiceConfig{
		Queries:     dbgen.New(pool),
		FuzzyRunner: catalog.PoolFuzzyRunner{Pool: pool},
	})
	require.NoError(t, err)
	f.svc = svc
	return f
}

func (f *searchFixture) insertTenant() string {
	f.t.Helper()
	var id string
	require.NoError(f.t, f.pool.QueryRow(context.Background(),
		`INSERT INTO tenants(slug,name) VALUES($1,$2) RETURNING id::text`, "it-search-"+randSuffix(), "IT search").Scan(&id))
	return id
}

func (f *searchFixture) insertNamed(table, tenantID, name, key string) *string {
	f.t.Helper()
	suffix := randSuffix()
	slug := key + "-" + suffix
	var id string
	require.NoError(f.t, f.pool.QueryRow(context.Background(),
		`INSERT INTO `+table+`(name,slug,tenant_id) VALUES($1,$2,$3) RETURNING id::text`, name+" "+suffix, slug, tenantID).Scan(&id))
	f.terms[key] = slug
	return &id
}

func (f *searchFixture) insertProduct(key, tenantID, title, description string, brandID, categoryID *string, price int64, inStock bool) {
	f.t.Helper()
	slug := key + "-" + randSuffix()
	var id string
	require.NoError(f.t, f.pool.QueryRow(context.Background(),
		`INSERT INTO products(title,slug,description,price,in_stock,brand_id,category_id,tenant_id)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`,
		title, slug, description, price, inStock, brandID, categoryID, tenantID).Scan(&id))
	f.ids[key] = id
	f.slugs[key] = slug
}

func (f *searchFixture) list(params catalog.ListParams) catalog.ProductListResult {
	f.t.Helper()
	if params.Page == 0 {
		params.Page = 1
	}
	if params.Limit == 0 {
		params.Limit = 20
	}
	res, err := f.svc.ListProducts(tenant.WithTenant(context.Background(), f.tenA), params)
	require.NoError(f.t, err)
	return res
}

func slugsOf(items []catalog.ProductListItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Slug)
	}
	return out
}

func TestIntegrationSearchSimpleConfig(t *testing.T) {
	f := newSearchFixture(t)
	for _, q := range []string{"sepatu", "Sepatu", "SEPATU lari"} {
		res := f.list(catalog.ListParams{Query: q})
		require.Equal(t, catalog.SearchModeFTS, res.SearchMode, q)
		require.Equal(t, []string{f.slugs["sepatu"]}, slugsOf(res.Items), q)
		require.EqualValues(t, 1, res.Total)
	}
	// Description is searched too ('simple' config keeps "katun" intact).
	res := f.list(catalog.ListParams{Query: "katun"})
	require.Equal(t, []string{f.slugs["kaos"]}, slugsOf(res.Items))
}

func TestIntegrationSearchFuzzyFallback(t *testing.T) {
	f := newSearchFixture(t)
	res := f.list(catalog.ListParams{Query: "spatu"})
	require.Equal(t, catalog.SearchModeFuzzy, res.SearchMode)
	// word_similarity('spatu', 'Sepatu Sandal Jepit') is 0.67 but that product belongs to tenant B.
	require.Equal(t, []string{f.slugs["sepatu"]}, slugsOf(res.Items))

	res = f.list(catalog.ListParams{Query: "hitem"})
	require.Equal(t, catalog.SearchModeFuzzy, res.SearchMode)
	require.Equal(t, []string{f.slugs["kaos"]}, slugsOf(res.Items))
	require.Empty(t, res.Suggestions)

	// Fuzzy still honours the other filters.
	res = f.list(catalog.ListParams{Query: "spatu", Category: f.terms["pakaian"]})
	require.EqualValues(t, 0, res.Total)
}

func TestIntegrationSearchSuggestions(t *testing.T) {
	f := newSearchFixture(t)
	// FTS: 'merah' and 'besar' are in no title -> 0 rows. Fuzzy: word_similarity against
	// 'Sepatu Lari Nike Pegasus' is 0.368 (< 0.4 threshold) -> 0 rows. Suggestions (>= 0.2) hit.
	res := f.list(catalog.ListParams{Query: "sepatu merah besar"})
	require.EqualValues(t, 0, res.Total)
	require.Empty(t, res.Items)
	require.Equal(t, catalog.SearchModeFTS, res.SearchMode)
	require.Len(t, res.Suggestions, 1, "tenant B's 'Sepatu Sandal Jepit' scores the same but must not leak")
	require.Equal(t, f.slugs["sepatu"], res.Suggestions[0].Slug)
	require.Equal(t, "Sepatu Lari Nike Pegasus", res.Suggestions[0].Title)
}

func TestIntegrationSearchFacets(t *testing.T) {
	f := newSearchFixture(t)
	res := f.list(catalog.ListParams{Category: f.terms["pakaian"], IncludeFacets: true})
	require.EqualValues(t, 2, res.Total)
	require.NotNil(t, res.Facets)

	cats := map[string]int64{}
	for _, c := range res.Facets.Categories {
		cats[c.Slug] = c.Count
	}
	require.Equal(t, map[string]int64{f.terms["pakaian"]: 2, f.terms["sepatu"]: 1}, cats, "category facet ignores the category filter")

	require.Len(t, res.Facets.Brands, 1)
	require.Equal(t, f.terms["acme"], res.Facets.Brands[0].Slug)
	require.EqualValues(t, 2, res.Facets.Brands[0].Count)

	require.Equal(t, catalog.InStockFacet{True: 1, False: 1}, res.Facets.InStock)

	buckets := res.Facets.Price.Buckets
	require.Len(t, buckets, 9)
	var sum int64
	for _, b := range buckets {
		sum += b.Count
	}
	require.EqualValues(t, 2, sum)
	require.EqualValues(t, 50_000, buckets[1].Min)
	require.EqualValues(t, 1, buckets[1].Count, "75_000 falls in [50000,100000)")
	require.EqualValues(t, 250_000, buckets[3].Min)
	require.EqualValues(t, 1, buckets[3].Count, "250_000 falls in [250000,500000)")

	// Price facet ignores the price filter; the brand facet honours it.
	min := int64(100_000)
	res = f.list(catalog.ListParams{MinPrice: &min, IncludeFacets: true})
	require.EqualValues(t, 2, res.Total)
	sum = 0
	for _, b := range res.Facets.Price.Buckets {
		sum += b.Count
	}
	require.EqualValues(t, 3, sum)
	brands := map[string]int64{}
	for _, b := range res.Facets.Brands {
		brands[b.Slug] = b.Count
	}
	require.Equal(t, map[string]int64{f.terms["nike"]: 1, f.terms["acme"]: 1}, brands)

	// Facets follow the fuzzy match mode.
	res = f.list(catalog.ListParams{Query: "hitem", IncludeFacets: true})
	require.Equal(t, catalog.SearchModeFuzzy, res.SearchMode)
	require.Len(t, res.Facets.Categories, 1)
	require.Equal(t, f.terms["pakaian"], res.Facets.Categories[0].Slug)
	require.EqualValues(t, 1, res.Facets.Categories[0].Count)
}

func (f *searchFixture) insertOrder(tenantID, status string, items map[string]int) string {
	f.t.Helper()
	ctx := context.Background()
	var orderID string
	require.NoError(f.t, f.pool.QueryRow(ctx,
		`INSERT INTO orders(tenant_id,status,pricing_subtotal,pricing_total) VALUES($1,$2::order_status,0,0) RETURNING id::text`,
		tenantID, status).Scan(&orderID))
	for key, qty := range items {
		_, err := f.pool.Exec(ctx,
			`INSERT INTO order_items(order_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'t',$3,$4::int,1,$4::bigint)`,
			orderID, f.ids[key], f.slugs[key], qty)
		require.NoError(f.t, err)
	}
	return orderID
}

func (f *searchFixture) setStatus(orderID, status string) {
	f.t.Helper()
	_, err := f.pool.Exec(context.Background(), `UPDATE orders SET status = $2::order_status WHERE id = $1`, orderID, status)
	require.NoError(f.t, err)
}

func (f *searchFixture) refresh() {
	f.t.Helper()
	r := catalog.PopularityRefresher{Pool: f.pool}
	for _, ten := range []string{f.tenA, f.tenB} {
		var id pgtype.UUID
		require.NoError(f.t, id.Scan(ten))
		refreshed, _, _, err := r.RefreshTenant(context.Background(), id)
		require.NoError(f.t, err)
		require.True(f.t, refreshed)
	}
}

// unitsSold returns -1 when the product has no product_sales row.
func (f *searchFixture) unitsSold(key string) int64 {
	f.t.Helper()
	var units int64
	err := f.pool.QueryRow(context.Background(), `SELECT units_sold FROM product_sales WHERE product_id = $1`, f.ids[key]).Scan(&units)
	if errors.Is(err, pgx.ErrNoRows) {
		return -1
	}
	require.NoError(f.t, err)
	return units
}

func TestIntegrationPopularitySortAndRefresh(t *testing.T) {
	f := newSearchFixture(t)

	kaosOrder := f.insertOrder(f.tenA, "PENDING_PAYMENT", map[string]int{"kaos": 5})
	kemejaOrder := f.insertOrder(f.tenA, "PENDING_PAYMENT", map[string]int{"kemeja": 2})
	f.setStatus(kemejaOrder, "PAID")
	// Tenant B sells a lot; must not influence tenant A.
	f.insertOrder(f.tenB, "DELIVERED", map[string]int{"sandal": 100})
	f.refresh()

	require.EqualValues(t, 2, f.unitsSold("kemeja"))
	require.EqualValues(t, -1, f.unitsSold("kaos"), "unpaid order is not counted")
	require.EqualValues(t, 100, f.unitsSold("sandal"))
	res := f.list(catalog.ListParams{Sort: "popularity:desc"})
	require.Equal(t, f.slugs["kemeja"], res.Items[0].Slug)

	// Paid -> shipped keeps the count; cancelled drops it (stale row removed).
	f.setStatus(kemejaOrder, "SHIPPED")
	f.refresh()
	require.EqualValues(t, 2, f.unitsSold("kemeja"))
	f.setStatus(kemejaOrder, "CANCELLED")
	f.refresh()
	require.EqualValues(t, -1, f.unitsSold("kemeja"))

	// Seeder sequence: order committed as PAID first, items inserted afterwards.
	seeded := f.insertOrder(f.tenA, "PAID", nil)
	_, err := f.pool.Exec(context.Background(),
		`INSERT INTO order_items(order_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'t',$3,5,1,5)`,
		seeded, f.ids["kaos"], f.slugs["kaos"])
	require.NoError(t, err)
	f.setStatus(kaosOrder, "DELIVERED")
	f.refresh()
	require.EqualValues(t, 10, f.unitsSold("kaos"))
	res = f.list(catalog.ListParams{Sort: "popularity:desc"})
	require.Equal(t, f.slugs["kaos"], res.Items[0].Slug)
	require.Len(t, res.Items, 3)

	// Refresh is idempotent.
	r := catalog.PopularityRefresher{Pool: f.pool}
	var tenA pgtype.UUID
	require.NoError(t, tenA.Scan(f.tenA))
	refreshed, upserted, deleted, err := r.RefreshTenant(context.Background(), tenA)
	require.NoError(t, err)
	require.True(t, refreshed)
	require.Zero(t, upserted)
	require.Zero(t, deleted)

	// A concurrent refresher holding the tenant lock makes RefreshTenant skip.
	tx, err := f.pool.Begin(context.Background())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	locked, err := dbgen.New(tx).TryLockProductSalesTenant(context.Background(), tenA)
	require.NoError(t, err)
	require.True(t, locked)
	refreshed, _, _, err = r.RefreshTenant(context.Background(), tenA)
	require.NoError(t, err)
	require.False(t, refreshed)
	require.NoError(t, tx.Rollback(context.Background()))

	// Deleting a product cascades its counter.
	_, err = f.pool.Exec(context.Background(), `DELETE FROM products WHERE id = $1`, f.ids["kaos"])
	require.NoError(t, err)
	require.EqualValues(t, -1, f.unitsSold("kaos"))
	// order_items keep the orphan product id (no FK); refresh skips it.
	f.refresh()
	require.EqualValues(t, -1, f.unitsSold("kaos"))
}

func TestIntegrationRatingSort(t *testing.T) {
	f := newSearchFixture(t)
	ctx := context.Background()
	review := func(productKey string, rating int) {
		var userID string
		require.NoError(t, f.pool.QueryRow(ctx,
			`INSERT INTO users(name,email,password_hash) VALUES('IT',$1,'x') RETURNING id::text`,
			"it-search-"+randSuffix()+"@example.test").Scan(&userID))
		f.users = append(f.users, userID)
		_, err := f.pool.Exec(ctx, `INSERT INTO reviews(product_id,user_id,rating,tenant_id) VALUES($1,$2,$3,$4)`,
			f.ids[productKey], userID, rating, f.tenA)
		require.NoError(t, err)
	}
	review("kaos", 5)
	review("kaos", 5)
	review("sepatu", 5)
	review("kemeja", 4)

	res := f.list(catalog.ListParams{Sort: "rating:desc"})
	// kaos and sepatu both average 5; kaos has more reviews.
	require.Equal(t, []string{f.slugs["kaos"], f.slugs["sepatu"], f.slugs["kemeja"]}, slugsOf(res.Items))
	require.InDelta(t, 5.0, res.Items[0].Rating, 0.001)
	require.Equal(t, 2, res.Items[0].ReviewCount)
}

// Cross-stream guard (catalog-ops adds products.status): when the column exists,
// a non-published product must not appear in data, facets or suggestions.
func TestIntegrationSearchHidesUnpublishedProducts(t *testing.T) {
	f := newSearchFixture(t)
	var hasStatus bool
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'products' AND column_name = 'status')`).Scan(&hasStatus))
	if !hasStatus {
		t.Skip("products.status not present on this branch (added by catalog-ops); nothing to hide")
	}
	_, err := f.pool.Exec(context.Background(), `UPDATE products SET status = 'draft' WHERE id = $1`, f.ids["kemeja"])
	require.NoError(t, err)

	res := f.list(catalog.ListParams{Query: "kemeja", IncludeFacets: true})
	require.EqualValues(t, 0, res.Total)
	require.Empty(t, res.Facets.Categories)
	require.Empty(t, res.Facets.Brands)
	for _, s := range res.Suggestions {
		require.NotEqual(t, f.slugs["kemeja"], s.Slug)
	}
	res = f.list(catalog.ListParams{Query: "kemejaa"})
	require.EqualValues(t, 0, res.Total, "fuzzy must not match a draft product")
	for _, s := range res.Suggestions {
		require.NotEqual(t, f.slugs["kemeja"], s.Slug)
	}
	res = f.list(catalog.ListParams{IncludeFacets: true})
	require.EqualValues(t, 2, res.Total)
	require.Equal(t, int64(2), res.Facets.InStock.True+res.Facets.InStock.False)
}
