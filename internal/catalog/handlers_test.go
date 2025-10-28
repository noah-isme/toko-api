package catalog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/catalog"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type productsResponse struct {
	Data       []catalog.ProductListItem `json:"data"`
	Pagination struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalItems int `json:"total_items"`
	} `json:"pagination"`
}

type productDetailResponse struct {
	Data catalog.ProductDetail `json:"data"`
}

type brandsResponse struct {
	Data []catalog.Brand `json:"data"`
}

type categoriesResponse struct {
	Data []catalog.Category `json:"data"`
}

type relatedResponse struct {
	Data []catalog.ProductListItem `json:"data"`
}

func TestCatalogHandlers(t *testing.T) {
	queries := newFakeCatalogQueries(t)
	svc, err := catalog.NewService(catalog.ServiceConfig{
		Queries:      queries,
		DefaultPage:  1,
		DefaultLimit: 20,
		MaxLimit:     100,
	})
	require.NoError(t, err)

	handler := catalog.NewHandler(catalog.HandlerConfig{Service: svc})

	t.Run("brands and categories", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/brands", nil)
		rec := httptest.NewRecorder()
		handler.Brands(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var br brandsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &br))
		require.Len(t, br.Data, 1)
		require.Equal(t, "Acme", br.Data[0].Name)

		creq := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
		crec := httptest.NewRecorder()
		handler.Categories(crec, creq)
		require.Equal(t, http.StatusOK, crec.Code)
		var cat categoriesResponse
		require.NoError(t, json.Unmarshal(crec.Body.Bytes(), &cat))
		require.Len(t, cat.Data, 1)
		require.Equal(t, "fashion", cat.Data[0].Slug)
	})

	t.Run("products list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products?limit=1", nil)
		rec := httptest.NewRecorder()
		handler.Products(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "2", rec.Header().Get("X-Total-Count"))

		var resp productsResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "Kaos Hitam", resp.Data[0].Title)
		require.Equal(t, 249000, int(resp.Data[0].Price))
		require.Equal(t, 1, resp.Pagination.Page)
		require.Equal(t, 1, resp.Pagination.PerPage)
		require.Equal(t, 2, resp.Pagination.TotalItems)
	})

	t.Run("product detail", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/kaos-hitam", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("slug", "kaos-hitam")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		rec := httptest.NewRecorder()
		handler.ProductDetail(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var resp productDetailResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, "Kaos Hitam", resp.Data.Title)
		require.NotNil(t, resp.Data.Brand)
		require.Equal(t, "acme", resp.Data.Brand.Slug)
		require.ElementsMatch(t, []string{"fashion"}, resp.Data.CategoryPath)
		require.Len(t, resp.Data.Variants, 1)
		require.Equal(t, "S", strings.ToUpper(*resp.Data.Variants[0].SKU))
		require.Len(t, resp.Data.Images, 1)
		require.Len(t, resp.Data.Specs, 1)
	})

	t.Run("related products", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products/kaos-hitam/related", nil)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("slug", "kaos-hitam")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
		rec := httptest.NewRecorder()
		handler.Related(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var resp relatedResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "Sepatu Putih", resp.Data[0].Title)
	})
}

type fakeCatalogQueries struct {
	brands         []dbgen.ListBrandsRow
	brandsByID     map[string]dbgen.GetBrandByIDRow
	categories     []dbgen.ListCategoriesRow
	categoriesByID map[string]dbgen.GetCategoryByIDRow
	productsBySlug map[string]dbgen.GetProductBySlugRow
	productList    []dbgen.ListProductsPublicRow
	variants       map[string][]dbgen.ProductVariant
	images         map[string][]dbgen.ProductImage
	specs          map[string][]dbgen.ProductSpec
	related        map[string][]dbgen.ListRelatedByCategoryRow
}

func newFakeCatalogQueries(t *testing.T) *fakeCatalogQueries {
	t.Helper()
	brandID := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	categoryID := mustUUID(t, "22222222-2222-2222-2222-222222222222")
	productID := mustUUID(t, "33333333-3333-3333-3333-333333333333")
	relatedID := mustUUID(t, "44444444-4444-4444-4444-444444444444")
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}

	listRow := dbgen.ListProductsPublicRow{
		ID:        productID,
		Title:     "Kaos Hitam",
		Slug:      "kaos-hitam",
		Price:     249000,
		CompareAt: pgtype.Int8{Int64: 299000, Valid: true},
		InStock:   true,
		Thumbnail: pgtype.Text{String: "https://cdn.example/kaos.jpg", Valid: true},
		Badges:    []string{"promo", "new"},
		CreatedAt: now,
	}
	relatedRow := dbgen.ListProductsPublicRow{
		ID:        relatedID,
		Title:     "Sepatu Putih",
		Slug:      "sepatu-putih",
		Price:     399000,
		CompareAt: pgtype.Int8{},
		InStock:   true,
		Thumbnail: pgtype.Text{},
		Badges:    nil,
		CreatedAt: now,
	}

	return &fakeCatalogQueries{
		brands: []dbgen.ListBrandsRow{{ID: brandID, Name: "Acme", Slug: "acme"}},
		brandsByID: map[string]dbgen.GetBrandByIDRow{
			uuidString(brandID): {ID: brandID, Name: "Acme", Slug: "acme"},
		},
		categories: []dbgen.ListCategoriesRow{{ID: categoryID, Name: "Fashion", Slug: "fashion", ParentID: pgtype.UUID{}}},
		categoriesByID: map[string]dbgen.GetCategoryByIDRow{
			uuidString(categoryID): {ID: categoryID, Name: "Fashion", Slug: "fashion", ParentID: pgtype.UUID{}},
		},
		productsBySlug: map[string]dbgen.GetProductBySlugRow{
			"kaos-hitam": {
				ID:         productID,
				Title:      "Kaos Hitam",
				Slug:       "kaos-hitam",
				Price:      249000,
				CompareAt:  pgtype.Int8{Int64: 299000, Valid: true},
				InStock:    true,
				Thumbnail:  pgtype.Text{String: "https://cdn.example/kaos.jpg", Valid: true},
				Badges:     []string{"promo", "new"},
				BrandID:    brandID,
				CategoryID: categoryID,
				CreatedAt:  now,
			},
			"sepatu-putih": {
				ID:         relatedID,
				Title:      "Sepatu Putih",
				Slug:       "sepatu-putih",
				Price:      399000,
				CompareAt:  pgtype.Int8{},
				InStock:    true,
				Thumbnail:  pgtype.Text{},
				Badges:     nil,
				BrandID:    pgtype.UUID{},
				CategoryID: categoryID,
				CreatedAt:  now,
			},
		},
		productList: []dbgen.ListProductsPublicRow{listRow, relatedRow},
		variants: map[string][]dbgen.ProductVariant{
			uuidString(productID): {{
				ID:         mustUUID(t, "55555555-5555-5555-5555-555555555555"),
				ProductID:  productID,
				Sku:        pgtype.Text{String: "s", Valid: true},
				Price:      249000,
				Stock:      10,
				Attributes: []byte(`{"size":"S"}`),
			}},
		},
		images: map[string][]dbgen.ProductImage{
			uuidString(productID): {{
				ID:        mustUUID(t, "66666666-6666-6666-6666-666666666666"),
				ProductID: productID,
				Url:       "https://cdn.example/kaos.jpg",
				SortOrder: 0,
			}},
		},
		specs: map[string][]dbgen.ProductSpec{
			uuidString(productID): {{
				ID:        mustUUID(t, "77777777-7777-7777-7777-777777777777"),
				ProductID: productID,
				Key:       "material",
				Value:     "katun",
			}},
		},
		related: map[string][]dbgen.ListRelatedByCategoryRow{
			uuidString(categoryID): {{
				ID:        relatedRow.ID,
				Title:     relatedRow.Title,
				Slug:      relatedRow.Slug,
				Price:     relatedRow.Price,
				CompareAt: relatedRow.CompareAt,
				InStock:   relatedRow.InStock,
				Thumbnail: relatedRow.Thumbnail,
				Badges:    relatedRow.Badges,
				CreatedAt: relatedRow.CreatedAt,
			}},
		},
	}
}

func (f *fakeCatalogQueries) ListBrands(context.Context) ([]dbgen.ListBrandsRow, error) {
	return append([]dbgen.ListBrandsRow(nil), f.brands...), nil
}

func (f *fakeCatalogQueries) GetBrandByID(ctx context.Context, id pgtype.UUID) (dbgen.GetBrandByIDRow, error) {
	row, ok := f.brandsByID[uuidString(id)]
	if !ok {
		return dbgen.GetBrandByIDRow{}, fmt.Errorf("brand not found")
	}
	return row, nil
}

func (f *fakeCatalogQueries) ListCategories(context.Context) ([]dbgen.ListCategoriesRow, error) {
	return append([]dbgen.ListCategoriesRow(nil), f.categories...), nil
}

func (f *fakeCatalogQueries) GetCategoryByID(ctx context.Context, id pgtype.UUID) (dbgen.GetCategoryByIDRow, error) {
	row, ok := f.categoriesByID[uuidString(id)]
	if !ok {
		return dbgen.GetCategoryByIDRow{}, fmt.Errorf("category not found")
	}
	return row, nil
}

func (f *fakeCatalogQueries) CountProductsPublic(ctx context.Context, arg dbgen.CountProductsPublicParams) (int64, error) {
	return int64(len(f.filterProducts(arg))), nil
}

func (f *fakeCatalogQueries) ListProductsPublic(ctx context.Context, arg dbgen.ListProductsPublicParams) ([]dbgen.ListProductsPublicRow, error) {
	filtered := f.filterProducts(dbgen.CountProductsPublicParams{
		Q:            arg.Q,
		CategorySlug: arg.CategorySlug,
		BrandSlug:    arg.BrandSlug,
		MinPrice:     arg.MinPrice,
		MaxPrice:     arg.MaxPrice,
		InStock:      arg.InStock,
	})
	start := int(arg.OffsetValue)
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + int(arg.LimitValue)
	if end > len(filtered) {
		end = len(filtered)
	}
	return append([]dbgen.ListProductsPublicRow(nil), filtered[start:end]...), nil
}

func (f *fakeCatalogQueries) GetProductBySlug(ctx context.Context, slug string) (dbgen.GetProductBySlugRow, error) {
	row, ok := f.productsBySlug[slug]
	if !ok {
		return dbgen.GetProductBySlugRow{}, fmt.Errorf("product not found")
	}
	return row, nil
}

func (f *fakeCatalogQueries) ListVariantsByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductVariant, error) {
	key := uuidString(productID)
	rows := f.variants[key]
	return append([]dbgen.ProductVariant(nil), rows...), nil
}

func (f *fakeCatalogQueries) ListImagesByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductImage, error) {
	key := uuidString(productID)
	rows := f.images[key]
	return append([]dbgen.ProductImage(nil), rows...), nil
}

func (f *fakeCatalogQueries) ListSpecsByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductSpec, error) {
	key := uuidString(productID)
	rows := f.specs[key]
	return append([]dbgen.ProductSpec(nil), rows...), nil
}

func (f *fakeCatalogQueries) ListRelatedByCategory(ctx context.Context, arg dbgen.ListRelatedByCategoryParams) ([]dbgen.ListRelatedByCategoryRow, error) {
	rows := f.related[uuidString(arg.CategoryID)]
	result := make([]dbgen.ListRelatedByCategoryRow, 0, len(rows))
	for _, row := range rows {
		if row.Slug == arg.Slug {
			continue
		}
		result = append(result, row)
	}
	return result, nil
}

func (f *fakeCatalogQueries) filterProducts(arg dbgen.CountProductsPublicParams) []dbgen.ListProductsPublicRow {
	result := make([]dbgen.ListProductsPublicRow, 0, len(f.productList))
	for _, row := range f.productList {
		if !matchesString(arg.Q, row.Title) {
			continue
		}
		if !matchesEqual(arg.CategorySlug, f.categorySlugForProduct(row.Slug)) {
			continue
		}
		if !matchesEqual(arg.BrandSlug, f.brandSlugForProduct(row.Slug)) {
			continue
		}
		if !matchesMin(arg.MinPrice, row.Price) || !matchesMax(arg.MaxPrice, row.Price) {
			continue
		}
		if arg.InStock != nil {
			if b, ok := arg.InStock.(bool); ok && b != row.InStock {
				continue
			}
		}
		result = append(result, row)
	}
	return result
}

func (f *fakeCatalogQueries) categorySlugForProduct(slug string) string {
	row, ok := f.productsBySlug[slug]
	if !ok || !row.CategoryID.Valid {
		return ""
	}
	cat, ok := f.categoriesByID[uuidString(row.CategoryID)]
	if !ok {
		return ""
	}
	return cat.Slug
}

func (f *fakeCatalogQueries) brandSlugForProduct(slug string) string {
	row, ok := f.productsBySlug[slug]
	if !ok || !row.BrandID.Valid {
		return ""
	}
	brand, ok := f.brandsByID[uuidString(row.BrandID)]
	if !ok {
		return ""
	}
	return brand.Slug
}

func matchesString(pattern any, value string) bool {
	if pattern == nil {
		return true
	}
	s, ok := pattern.(string)
	if !ok {
		return true
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(s))
}

func matchesEqual(pattern any, value string) bool {
	if pattern == nil {
		return true
	}
	s, ok := pattern.(string)
	if !ok || s == "" {
		return true
	}
	return strings.EqualFold(s, value)
}

func matchesMin(pattern any, price int64) bool {
	if pattern == nil {
		return true
	}
	if v, ok := pattern.(int64); ok {
		return price >= v
	}
	return true
}

func matchesMax(pattern any, price int64) bool {
	if pattern == nil {
		return true
	}
	if v, ok := pattern.(int64); ok {
		return price <= v
	}
	return true
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
