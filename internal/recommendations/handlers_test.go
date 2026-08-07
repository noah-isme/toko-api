package recommendations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/recommendations"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type recommendationResponse struct {
	Data []recommendations.ProductListItem `json:"data"`
}

func TestRecommendationHandlers(t *testing.T) {
	queries := newFakeRecommendationQueries(t)
	svc, err := recommendations.NewService(recommendations.ServiceConfig{
		Queries: queries,
	})
	require.NoError(t, err)

	handler := recommendations.NewHandler(recommendations.HandlerConfig{Service: svc})

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	userID := pgtype.UUID{Bytes: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Valid: true}
	productID := pgtype.UUID{Bytes: uuid.MustParse("33333333-3333-3333-3333-333333333333"), Valid: true}

	t.Run("personalized recommendations for authenticated user", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations/personalized?limit=5", nil)
		req = req.WithContext(context.WithValue(req.Context(), recommendations.UserIDKey{}, userID.String()))
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.Personalized(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "Personalized Product", resp.Data[0].Title)
		require.Equal(t, "personalized-product", resp.Data[0].Slug)
		require.Equal(t, int64(10000), resp.Data[0].Price)
		require.Equal(t, 4.5, resp.Data[0].Rating)
		require.True(t, resp.Data[0].InStock)
	})

	t.Run("personalized recommendations for anonymous user falls back to trending", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations/personalized?limit=5", nil)
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.Personalized(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "Trending Product", resp.Data[0].Title)
	})

	t.Run("trending products", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations/trending?limit=5", nil)
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.Trending(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "Trending Product", resp.Data[0].Title)
	})

	t.Run("frequently bought together with UUID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/"+productID.String()+"/frequently-bought-together", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", productID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.FrequentlyBoughtTogether(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "FBT Product", resp.Data[0].Title)
	})

	t.Run("frequently bought together with slug", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/test-product/frequently-bought-together", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", "test-product")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.FrequentlyBoughtTogether(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		// Same product (test-product), so same FBT data as UUID lookup
		require.Equal(t, "FBT Product", resp.Data[0].Title)
	})

	t.Run("customers also viewed with UUID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/"+productID.String()+"/also-viewed", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", productID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.CustomersAlsoViewed(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "Also Viewed Product", resp.Data[0].Title)
	})

	t.Run("customers also viewed with slug", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/test-product/also-viewed", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", "test-product")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.CustomersAlsoViewed(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		// Same product (test-product), so same CAV data as UUID lookup
		require.Equal(t, "Also Viewed Product", resp.Data[0].Title)
	})

	t.Run("frequently bought together returns empty for nonexistent product", func(t *testing.T) {
		nonexistentUUID := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff").String()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/"+nonexistentUUID+"/frequently-bought-together", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", nonexistentUUID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		req = req.WithContext(context.WithValue(req.Context(), "tenant_id", tenantID.String()))
		rec := httptest.NewRecorder()
		handler.FrequentlyBoughtTogether(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var resp recommendationResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Empty(t, resp.Data)
	})
}

type fakeRecommendationQueries struct {
	personalized []dbgen.GetPersonalizedRecommendationsRow
	trending     []dbgen.GetTrendingProductsRow
	fbtByUUID    map[string][]dbgen.GetFrequentlyBoughtTogetherRow
	fbtBySlug    map[string][]dbgen.GetFrequentlyBoughtTogetherRow
	cavByUUID    map[string][]dbgen.GetCustomersAlsoViewedRow
	cavBySlug    map[string][]dbgen.GetCustomersAlsoViewedRow
	productByID  map[string]dbgen.GetProductForCartRow
	productBySlug map[string]dbgen.GetProductBySlugRow
}

func newFakeRecommendationQueries(t *testing.T) *fakeRecommendationQueries {
	t.Helper()
	tenantID := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	userID := mustUUID(t, "22222222-2222-2222-2222-222222222222")
	productID := mustUUID(t, "33333333-3333-3333-3333-333333333333")
	fbtProductID := mustUUID(t, "44444444-4444-4444-4444-444444444444")
	cavProductID := mustUUID(t, "55555555-5555-5555-5555-555555555555")
	_ = tenantID
	_ = userID
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}

	personalizedRow := dbgen.GetPersonalizedRecommendationsRow{
		ID:          productID,
		Title:       "Personalized Product",
		Slug:        "personalized-product",
		Price:       10000,
		InStock:     true,
		Rating:      4.5,
		Thumbnail:   pgtype.Text{String: "personalized.jpg", Valid: true},
		CategoryID:  pgtype.UUID{Valid: false},
		CategoryName: pgtype.Text{Valid: false},
		BrandID:     pgtype.UUID{Valid: false},
		BrandName:   pgtype.Text{Valid: false},
		CreatedAt:   now,
		ReviewCount: 10,
		TotalStock:  5,
		Score:       2.0,
	}

	trendingRow := dbgen.GetTrendingProductsRow{
		ID:          mustUUID(t, "66666666-6666-6666-6666-666666666666"),
		Title:       "Trending Product",
		Slug:        "trending-product",
		Price:       20000,
		InStock:     true,
		Rating:      4.8,
		Thumbnail:   pgtype.Text{String: "trending.jpg", Valid: true},
		CategoryID:  pgtype.UUID{Valid: false},
		CategoryName: pgtype.Text{Valid: false},
		BrandID:     pgtype.UUID{Valid: false},
		BrandName:   pgtype.Text{Valid: false},
		CreatedAt:   now,
		ReviewCount: 20,
		TotalStock:  3,
	}

	fbtRow := dbgen.GetFrequentlyBoughtTogetherRow{
		ID:          fbtProductID,
		Title:       "FBT Product",
		Slug:        "fbt-product",
		Price:       25000,
		InStock:     true,
		Rating:      4.6,
		Thumbnail:   pgtype.Text{String: "fbt.jpg", Valid: true},
		CategoryID:  pgtype.UUID{Valid: false},
		CategoryName: pgtype.Text{Valid: false},
		BrandID:     pgtype.UUID{Valid: false},
		BrandName:   pgtype.Text{Valid: false},
		CreatedAt:   now,
		ReviewCount: 8,
		TotalStock:  7,
		PairCount:   50,
	}

	fbtSlugRow := dbgen.GetFrequentlyBoughtTogetherRow{
		ID:          mustUUID(t, "77777777-7777-7777-7777-777777777777"),
		Title:       "FBT by Slug",
		Slug:        "fbt-by-slug",
		Price:       30000,
		InStock:     true,
		Rating:      4.4,
		Thumbnail:   pgtype.Text{String: "fbt2.jpg", Valid: true},
		CategoryID:  pgtype.UUID{Valid: false},
		CategoryName: pgtype.Text{Valid: false},
		BrandID:     pgtype.UUID{Valid: false},
		BrandName:   pgtype.Text{Valid: false},
		CreatedAt:   now,
		ReviewCount: 12,
		TotalStock:  4,
		PairCount:   30,
	}

	cavRow := dbgen.GetCustomersAlsoViewedRow{
		ID:          cavProductID,
		Title:       "Also Viewed Product",
		Slug:        "also-viewed",
		Price:       18000,
		InStock:     true,
		Rating:      4.3,
		Thumbnail:   pgtype.Text{String: "also.jpg", Valid: true},
		CategoryID:  pgtype.UUID{Valid: false},
		CategoryName: pgtype.Text{Valid: false},
		BrandID:     pgtype.UUID{Valid: false},
		BrandName:   pgtype.Text{Valid: false},
		CreatedAt:   now,
		ReviewCount: 5,
		TotalStock:  6,
	}

	cavSlugRow := dbgen.GetCustomersAlsoViewedRow{
		ID:          mustUUID(t, "88888888-8888-8888-8888-888888888888"),
		Title:       "CAV by Slug",
		Slug:        "cav-by-slug",
		Price:       22000,
		InStock:     true,
		Rating:      4.2,
		Thumbnail:   pgtype.Text{String: "cav.jpg", Valid: true},
		CategoryID:  pgtype.UUID{Valid: false},
		CategoryName: pgtype.Text{Valid: false},
		BrandID:     pgtype.UUID{Valid: false},
		BrandName:   pgtype.Text{Valid: false},
		CreatedAt:   now,
		ReviewCount: 7,
		TotalStock:  8,
	}

	productByID := dbgen.GetProductForCartRow{
		ID:         productID,
		Title:      "Test Product",
		Slug:       "test-product",
		Price:      10000,
		CategoryID: pgtype.UUID{Valid: false},
		BrandID:    pgtype.UUID{Valid: false},
		InStock:    true,
	}

	productBySlug := dbgen.GetProductBySlugRow{
		ID:         productID,
		Title:      "Test Product",
		Slug:       "test-product",
		Price:      10000,
		CompareAt:  pgtype.Int8{},
		InStock:    true,
		Thumbnail:  pgtype.Text{},
		Badges:     nil,
		BrandID:    pgtype.UUID{Valid: false},
		CategoryID: pgtype.UUID{Valid: false},
		CreatedAt:  now,
		TotalStock: 10,
	}

	return &fakeRecommendationQueries{
		personalized: []dbgen.GetPersonalizedRecommendationsRow{personalizedRow},
		trending:     []dbgen.GetTrendingProductsRow{trendingRow},
		fbtByUUID: map[string][]dbgen.GetFrequentlyBoughtTogetherRow{
			uuidString(productID):          {fbtRow},
			uuidString(fbtProductID):       {fbtSlugRow}, // slug-resolved product
		},
		fbtBySlug: map[string][]dbgen.GetFrequentlyBoughtTogetherRow{
			"test-product": {fbtSlugRow},
		},
		cavByUUID: map[string][]dbgen.GetCustomersAlsoViewedRow{
			uuidString(productID):         {cavRow},
			uuidString(cavProductID):      {cavSlugRow}, // slug-resolved product
		},
		cavBySlug: map[string][]dbgen.GetCustomersAlsoViewedRow{
			"test-product": {cavSlugRow},
		},
		productByID: map[string]dbgen.GetProductForCartRow{
			uuidString(productID):   productByID,
			uuidString(fbtProductID): {ID: fbtProductID, Title: "FBT by Slug", Slug: "fbt-by-slug", Price: 30000, InStock: true},
			uuidString(cavProductID): {ID: cavProductID, Title: "CAV by Slug", Slug: "cav-by-slug", Price: 22000, InStock: true},
		},
		productBySlug: map[string]dbgen.GetProductBySlugRow{
			"test-product": productBySlug,
			"fbt-by-slug": {ID: fbtProductID, Title: "FBT by Slug", Slug: "fbt-by-slug", Price: 30000, InStock: true, CreatedAt: now},
			"cav-by-slug": {ID: cavProductID, Title: "CAV by Slug", Slug: "cav-by-slug", Price: 22000, InStock: true, CreatedAt: now},
		},
	}
}

func (f *fakeRecommendationQueries) ListProductsPublic(ctx context.Context, arg dbgen.ListProductsPublicParams) ([]dbgen.ListProductsPublicRow, error) {
	return nil, nil
}

func (f *fakeRecommendationQueries) GetProductBySlug(ctx context.Context, arg dbgen.GetProductBySlugParams) (dbgen.GetProductBySlugRow, error) {
	row, ok := f.productBySlug[arg.Slug]
	if !ok {
		return dbgen.GetProductBySlugRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeRecommendationQueries) GetProductForCart(ctx context.Context, arg dbgen.GetProductForCartParams) (dbgen.GetProductForCartRow, error) {
	key := uuidString(arg.ID)
	row, ok := f.productByID[key]
	if !ok {
		return dbgen.GetProductForCartRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeRecommendationQueries) GetFrequentlyBoughtTogether(ctx context.Context, arg dbgen.GetFrequentlyBoughtTogetherParams) ([]dbgen.GetFrequentlyBoughtTogetherRow, error) {
	key := uuidString(arg.ProductIDA)
	rows, ok := f.fbtByUUID[key]
	if !ok {
		return []dbgen.GetFrequentlyBoughtTogetherRow{}, nil
	}
	return append([]dbgen.GetFrequentlyBoughtTogetherRow(nil), rows...), nil
}

func (f *fakeRecommendationQueries) GetCustomersAlsoViewed(ctx context.Context, arg dbgen.GetCustomersAlsoViewedParams) ([]dbgen.GetCustomersAlsoViewedRow, error) {
	key := uuidString(arg.ProductID)
	rows, ok := f.cavByUUID[key]
	if !ok {
		return []dbgen.GetCustomersAlsoViewedRow{}, nil
	}
	return append([]dbgen.GetCustomersAlsoViewedRow(nil), rows...), nil
}

func (f *fakeRecommendationQueries) GetPersonalizedRecommendations(ctx context.Context, arg dbgen.GetPersonalizedRecommendationsParams) ([]dbgen.GetPersonalizedRecommendationsRow, error) {
	return append([]dbgen.GetPersonalizedRecommendationsRow(nil), f.personalized...), nil
}

func (f *fakeRecommendationQueries) GetTrendingProducts(ctx context.Context, arg dbgen.GetTrendingProductsParams) ([]dbgen.GetTrendingProductsRow, error) {
	return append([]dbgen.GetTrendingProductsRow(nil), f.trending...), nil
}

func (f *fakeRecommendationQueries) UpsertUserProductView(ctx context.Context, arg dbgen.UpsertUserProductViewParams) (dbgen.UserProductView, error) {
	return dbgen.UserProductView{}, nil
}

func (f *fakeRecommendationQueries) UpsertOrderProductPair(ctx context.Context, arg dbgen.UpsertOrderProductPairParams) (dbgen.OrderProductPair, error) {
	return dbgen.OrderProductPair{}, nil
}

func mustUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	require.NoError(t, id.Scan(value))
	return id
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	u, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return ""
	}
	return u.String()
}