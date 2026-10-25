package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/catalog"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type fakeFuzzyRunner struct {
	q     *fakeCatalogQueries
	calls int
}

func (f *fakeFuzzyRunner) RunFuzzy(ctx context.Context, fn func(catalog.QueryProvider) error) error {
	f.calls++
	return fn(f.q)
}

type searchResponse struct {
	Data       []catalog.ProductListItem `json:"data"`
	Pagination struct {
		TotalItems int `json:"totalItems"`
	} `json:"pagination"`
	Meta *struct {
		SearchMode  string               `json:"searchMode"`
		Suggestions []catalog.Suggestion `json:"suggestions"`
	} `json:"meta"`
	Facets *catalog.Facets `json:"facets"`
}

func newSearchHarness(t *testing.T) (*catalog.Handler, *fakeCatalogQueries, *fakeFuzzyRunner) {
	t.Helper()
	queries := newFakeCatalogQueries(t)
	runner := &fakeFuzzyRunner{q: queries}
	svc, err := catalog.NewService(catalog.ServiceConfig{
		Queries:      queries,
		FuzzyRunner:  runner,
		DefaultPage:  1,
		DefaultLimit: 20,
		MaxLimit:     100,
	})
	require.NoError(t, err)
	return catalog.NewHandler(catalog.HandlerConfig{Service: svc}), queries, runner
}

func doProducts(t *testing.T, h *catalog.Handler, query string) (*httptest.ResponseRecorder, map[string]json.RawMessage, searchResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/products"+query, nil)
	rec := httptest.NewRecorder()
	h.Products(rec, req)
	raw := map[string]json.RawMessage{}
	var resp searchResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec, raw, resp
}

func TestCatalogSearchSortNormalisation(t *testing.T) {
	h, queries, _ := newSearchHarness(t)
	cases := map[string]string{
		"popularity:desc": "popularity:desc",
		"popularity":      "popularity:desc",
		"POPULARITY":      "popularity:desc",
		"popular":         "",
		"rating":          "rating:desc",
		"rating:desc":     "rating:desc",
		"rating:asc":      "",
		"price:asc":       "price:asc",
		"bogus":           "",
	}
	for in, want := range cases {
		rec, _, _ := doProducts(t, h, "?sort="+in)
		require.Equal(t, http.StatusOK, rec.Code, in)
		require.Equal(t, want, queries.lastList.Sort, "sort=%s", in)
	}
}

func TestCatalogSearchFuzzyFallback(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	queries.fuzzyProducts = []dbgen.ListProductsPublicRow{queries.productList[1]}

	rec, _, resp := doProducts(t, h, "?q=spatu")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, "1", rec.Header().Get("X-Total-Count"))
	require.NotNil(t, resp.Meta)
	require.Equal(t, catalog.SearchModeFuzzy, resp.Meta.SearchMode)
	require.Len(t, resp.Data, 1)
	require.Equal(t, "sepatu-putih", resp.Data[0].Slug)
	require.Empty(t, resp.Meta.Suggestions)
	require.Equal(t, []string{catalog.SearchModeFTS, catalog.SearchModeFuzzy}, queries.countModes)
	require.Equal(t, 0, queries.suggestCalls)
}

func TestCatalogSearchShortQuerySkipsFuzzy(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	queries.fuzzyProducts = []dbgen.ListProductsPublicRow{queries.productList[1]}
	queries.suggestions = []dbgen.SuggestProductTitlesRow{{Title: "Kaos Hitam", Slug: "kaos-hitam"}}

	rec, raw, resp := doProducts(t, h, "?q=zq")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, runner.calls)
	require.Equal(t, 0, queries.suggestCalls)
	require.NotNil(t, resp.Meta)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["meta"], &meta))
	require.NotContains(t, meta, "suggestions")
}

func TestCatalogSearchFTSHitSkipsFuzzy(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	rec, _, resp := doProducts(t, h, "?q=sepatu")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, runner.calls)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	require.Len(t, resp.Data, 1)
	require.Equal(t, catalog.SearchModeFTS, queries.lastList.MatchMode)
	require.Equal(t, "sepatu", queries.lastList.Q.String)
}

func TestCatalogSearchSuggestions(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	queries.suggestions = []dbgen.SuggestProductTitlesRow{{Title: "Kaos Hitam", Slug: "kaos-hitam", Score: 0.3}}

	rec, _, resp := doProducts(t, h, "?q=zzzz")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, 0, resp.Pagination.TotalItems)
	require.Empty(t, resp.Data)
	require.NotNil(t, resp.Meta)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	require.Len(t, resp.Meta.Suggestions, 1)
	require.Equal(t, "kaos-hitam", resp.Meta.Suggestions[0].Slug)
	require.Equal(t, "zzzz", queries.lastSuggest.Q)
	require.EqualValues(t, 5, queries.lastSuggest.LimitValue)
	// Suggestions run inside the fuzzy transaction with the trigram threshold lowered to the
	// suggestion floor, so SuggestProductTitles can use the trigram index.
	require.Equal(t, []string{"0.2"}, queries.thresholds)

	// With results, suggestions are not computed.
	queries.suggestCalls = 0
	rec, _, resp = doProducts(t, h, "?q=kaos")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, queries.suggestCalls)
	require.Empty(t, resp.Meta.Suggestions)
}

func TestCatalogSearchResponseShapeWithoutQueryOrFacets(t *testing.T) {
	h, _, _ := newSearchHarness(t)
	rec, raw, _ := doProducts(t, h, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, raw, "meta")
	require.NotContains(t, raw, "facets")
	require.Contains(t, raw, "data")
	require.Contains(t, raw, "pagination")
}

func TestCatalogSearchFacets(t *testing.T) {
	h, queries, _ := newSearchHarness(t)
	catID := mustUUID(t, "22222222-2222-2222-2222-222222222222")
	brandID := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	queries.facetCategories = []dbgen.FacetCategoriesPublicRow{{ID: catID, Name: "Fashion", Slug: "fashion", Count: 2}}
	queries.facetBrands = []dbgen.FacetBrandsPublicRow{{ID: brandID, Name: "Acme", Slug: "acme", Count: 1}}
	queries.facetPrices = []dbgen.FacetPriceBucketsPublicRow{{Bucket: 2, Count: 1}, {Bucket: 3, Count: 1}, {Bucket: 8, Count: 4}}
	queries.facetStock = []dbgen.FacetInStockPublicRow{{InStock: true, Count: 2}, {InStock: false, Count: 1}}

	rec, raw, resp := doProducts(t, h, "?include=facets&category=fashion&brand=acme&minPrice=1000&inStock=true")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, raw, "meta")
	require.NotNil(t, resp.Facets)
	require.Len(t, resp.Facets.Categories, 1)
	require.Equal(t, "fashion", resp.Facets.Categories[0].Slug)
	require.EqualValues(t, 2, resp.Facets.Categories[0].Count)
	require.Len(t, resp.Facets.Brands, 1)
	require.Equal(t, int64(2), resp.Facets.InStock.True)
	require.Equal(t, int64(1), resp.Facets.InStock.False)

	buckets := resp.Facets.Price.Buckets
	require.Len(t, buckets, 9)
	require.EqualValues(t, 0, buckets[0].Min)
	require.NotNil(t, buckets[0].Max)
	require.EqualValues(t, 50000, *buckets[0].Max)
	require.EqualValues(t, 100000, buckets[2].Min)
	require.EqualValues(t, 1, buckets[2].Count)
	require.EqualValues(t, 1, buckets[3].Count)
	require.EqualValues(t, 10000000, buckets[8].Min)
	require.Nil(t, buckets[8].Max)
	require.EqualValues(t, 4, buckets[8].Count)

	// The raw JSON keeps max:null for the open-ended bucket and true/false keys.
	var facetsRaw struct {
		Price struct {
			Buckets []map[string]any `json:"buckets"`
		} `json:"price"`
		InStock map[string]int64 `json:"inStock"`
	}
	require.NoError(t, json.Unmarshal(raw["facets"], &facetsRaw))
	last := facetsRaw.Price.Buckets[8]
	require.Contains(t, last, "max")
	require.Nil(t, last["max"])
	require.Equal(t, map[string]int64{"true": 2, "false": 1}, facetsRaw.InStock)

	// Own-dimension exclusion: each facet carries every filter except its own.
	require.NotNil(t, queries.lastFacetCat)
	require.Equal(t, pgtype.Text{String: "acme", Valid: true}, queries.lastFacetCat.BrandSlug)
	require.Equal(t, pgtype.Bool{Bool: true, Valid: true}, queries.lastFacetCat.InStock)
	require.Equal(t, pgtype.Int8{Int64: 1000, Valid: true}, queries.lastFacetCat.MinPrice)
	require.NotNil(t, queries.lastFacetBrand)
	require.Equal(t, pgtype.Text{String: "fashion", Valid: true}, queries.lastFacetBrand.CategorySlug)
	require.NotNil(t, queries.lastFacetPrice)
	require.Equal(t, pgtype.Text{String: "fashion", Valid: true}, queries.lastFacetPrice.CategorySlug)
	require.Equal(t, pgtype.Text{String: "acme", Valid: true}, queries.lastFacetPrice.BrandSlug)
	require.Equal(t, catalog.DefaultPriceBucketEdges, queries.lastFacetPrice.Edges)
	require.NotNil(t, queries.lastFacetStock)
	require.Equal(t, pgtype.Int8{Int64: 1000, Valid: true}, queries.lastFacetStock.MinPrice)
	require.Equal(t, pgtype.Text{String: "acme", Valid: true}, queries.lastFacetStock.BrandSlug)
}

func TestCatalogSearchFacetsEmptyAreArrays(t *testing.T) {
	h, _, _ := newSearchHarness(t)
	rec, raw, _ := doProducts(t, h, "?include=facets")
	require.Equal(t, http.StatusOK, rec.Code)
	var facets map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["facets"], &facets))
	require.JSONEq(t, "[]", string(facets["categories"]))
	require.JSONEq(t, "[]", string(facets["brands"]))
}

func TestCatalogSearchFuzzyFacetsUseFuzzyMode(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	queries.fuzzyProducts = []dbgen.ListProductsPublicRow{queries.productList[0]}
	rec, _, resp := doProducts(t, h, "?q=kaoss+hitm&include=facets")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, catalog.SearchModeFuzzy, resp.Meta.SearchMode)
	require.NotNil(t, queries.lastFacetCat)
	require.Equal(t, catalog.SearchModeFuzzy, queries.lastFacetCat.MatchMode)
	require.Equal(t, catalog.SearchModeFuzzy, queries.lastFacetStock.MatchMode)
}

func TestCatalogSearchIncludeValidation(t *testing.T) {
	h, _, _ := newSearchHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/products?include=bogus", nil)
	rec := httptest.NewRecorder()
	h.Products(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var errResp struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	require.Equal(t, "BAD_REQUEST", errResp.Error.Code)
	require.Equal(t, "include", errResp.Error.Details["field"])

	rec2, raw, _ := doProducts(t, h, "?include=facets,")
	require.Equal(t, http.StatusOK, rec2.Code)
	require.Contains(t, raw, "facets")

	rec3, raw3, _ := doProducts(t, h, "?include=")
	require.Equal(t, http.StatusOK, rec3.Code)
	require.NotContains(t, raw3, "facets")
}

func TestCatalogSearchWithoutFuzzyRunner(t *testing.T) {
	queries := newFakeCatalogQueries(t)
	queries.fuzzyProducts = []dbgen.ListProductsPublicRow{queries.productList[0]}
	queries.suggestions = []dbgen.SuggestProductTitlesRow{{Title: "Kaos Hitam", Slug: "kaos-hitam"}}
	svc, err := catalog.NewService(catalog.ServiceConfig{Queries: queries})
	require.NoError(t, err)
	h := catalog.NewHandler(catalog.HandlerConfig{Service: svc})
	rec, _, resp := doProducts(t, h, "?q=spatu")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	require.Empty(t, resp.Data)
	// Suggestions need the time-limited fuzzy transaction, so none without a runner.
	require.Equal(t, 0, queries.suggestCalls)
	require.Empty(t, resp.Meta.Suggestions)
}

func TestCatalogNewServiceRejectsUnsortedEdges(t *testing.T) {
	_, err := catalog.NewService(catalog.ServiceConfig{Queries: newFakeCatalogQueries(t), PriceBucketEdges: []int64{100, 50}})
	require.Error(t, err)
}

func decodeErrorField(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var errResp struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	require.Equal(t, "BAD_REQUEST", errResp.Error.Code)
	field, _ := errResp.Error.Details["field"].(string)
	return field
}

func TestCatalogSearchQueryLengthCap(t *testing.T) {
	h, queries, runner := newSearchHarness(t)

	// MaxQueryRunes counts runes, not bytes: 200 two-byte runes are accepted.
	rec, _, _ := doProducts(t, h, "?q="+url.QueryEscape(strings.Repeat("é", catalog.MaxQueryRunes)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// Surrounding whitespace is trimmed before the check.
	rec, _, _ = doProducts(t, h, "?q="+url.QueryEscape("  "+strings.Repeat("a", catalog.MaxQueryRunes)+"  "))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	queries.countModes = nil
	rec, _, _ = doProducts(t, h, "?q="+url.QueryEscape(strings.Repeat("a", catalog.MaxQueryRunes+1)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "q", decodeErrorField(t, rec))
	require.Empty(t, queries.countModes, "an over-long q must not reach the database")
	require.Equal(t, 0, runner.calls)
}

func TestCatalogSearchLongQuerySkipsFuzzyAndSuggestions(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	queries.fuzzyProducts = []dbgen.ListProductsPublicRow{queries.productList[1]}
	queries.suggestions = []dbgen.SuggestProductTitlesRow{{Title: "Kaos Hitam", Slug: "kaos-hitam"}}

	// 65 runes: full-text search only (trigram cost grows with len(q)).
	rec, raw, resp := doProducts(t, h, "?q="+strings.Repeat("z", 65))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, runner.calls)
	require.Equal(t, 0, queries.suggestCalls)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	require.Empty(t, resp.Data)
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["meta"], &meta))
	require.NotContains(t, meta, "suggestions")

	// 64 runes is still eligible.
	rec, _, resp = doProducts(t, h, "?q="+strings.Repeat("z", 64))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, runner.calls)
	require.Equal(t, catalog.SearchModeFuzzy, resp.Meta.SearchMode)
}

func statementTimeoutErr() error {
	return &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
}

func TestCatalogSearchFuzzyTimeoutDegradesToFTS(t *testing.T) {
	h, queries, runner := newSearchHarness(t)
	queries.fuzzyProducts = []dbgen.ListProductsPublicRow{queries.productList[1]}
	queries.suggestions = []dbgen.SuggestProductTitlesRow{{Title: "Kaos Hitam", Slug: "kaos-hitam"}}
	queries.facetStock = []dbgen.FacetInStockPublicRow{{InStock: true, Count: 0}}
	queries.fuzzyCountErr = statementTimeoutErr()

	rec, _, resp := doProducts(t, h, "?q=spatu&include=facets")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, runner.calls)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	require.Empty(t, resp.Data)
	require.Empty(t, resp.Meta.Suggestions)
	require.NotNil(t, resp.Facets)
	// Facets are recomputed in FTS mode so they match data.
	require.Equal(t, catalog.SearchModeFTS, queries.lastFacetStock.MatchMode)
}

func TestCatalogSearchSuggestionTimeoutDegrades(t *testing.T) {
	h, queries, _ := newSearchHarness(t)
	queries.suggestErr = statementTimeoutErr()
	rec, raw, resp := doProducts(t, h, "?q=zzzz")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, queries.suggestCalls)
	require.Equal(t, catalog.SearchModeFTS, resp.Meta.SearchMode)
	var meta map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["meta"], &meta))
	require.NotContains(t, meta, "suggestions")
}

func TestCatalogSearchFuzzyErrorsStillFail(t *testing.T) {
	// Any error other than a server-side statement timeout is a real failure.
	queries := newFakeCatalogQueries(t)
	queries.fuzzyCountErr = errors.New("connection reset")
	svc, err := catalog.NewService(catalog.ServiceConfig{Queries: queries, FuzzyRunner: &fakeFuzzyRunner{q: queries}})
	require.NoError(t, err)
	ctx := tenant.WithTenant(context.Background(), "55555555-5555-5555-5555-555555555555")
	_, err = svc.ListProducts(ctx, catalog.ListParams{Query: "spatu", Page: 1, Limit: 20})
	require.Error(t, err)

	// A 57014 caused by the client going away (context cancelled) is not degraded either.
	queries.fuzzyCountErr = statementTimeoutErr()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = svc.ListProducts(cctx, catalog.ListParams{Query: "spatu", Page: 1, Limit: 20})
	require.Error(t, err)
}
