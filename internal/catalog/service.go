package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// QueryProvider is the subset of dbgen.Querier the catalog service needs; *dbgen.Queries satisfies it.
type QueryProvider interface {
	ListBrands(ctx context.Context, tenantID pgtype.UUID) ([]dbgen.ListBrandsRow, error)
	GetBrandByID(ctx context.Context, arg dbgen.GetBrandByIDParams) (dbgen.GetBrandByIDRow, error)
	ListCategories(ctx context.Context, tenantID pgtype.UUID) ([]dbgen.ListCategoriesRow, error)
	GetCategoryByID(ctx context.Context, arg dbgen.GetCategoryByIDParams) (dbgen.GetCategoryByIDRow, error)
	CountProductsPublic(ctx context.Context, arg dbgen.CountProductsPublicParams) (int64, error)
	ListProductsPublic(ctx context.Context, arg dbgen.ListProductsPublicParams) ([]dbgen.ListProductsPublicRow, error)
	GetProductBySlug(ctx context.Context, arg dbgen.GetProductBySlugParams) (dbgen.GetProductBySlugRow, error)
	ListVariantsByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductVariant, error)
	ListImagesByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductImage, error)
	ListSpecsByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductSpec, error)
	ListRelatedByCategory(ctx context.Context, arg dbgen.ListRelatedByCategoryParams) ([]dbgen.ListRelatedByCategoryRow, error)
	FacetCategoriesPublic(ctx context.Context, arg dbgen.FacetCategoriesPublicParams) ([]dbgen.FacetCategoriesPublicRow, error)
	FacetBrandsPublic(ctx context.Context, arg dbgen.FacetBrandsPublicParams) ([]dbgen.FacetBrandsPublicRow, error)
	FacetPriceBucketsPublic(ctx context.Context, arg dbgen.FacetPriceBucketsPublicParams) ([]dbgen.FacetPriceBucketsPublicRow, error)
	FacetInStockPublic(ctx context.Context, arg dbgen.FacetInStockPublicParams) ([]dbgen.FacetInStockPublicRow, error)
	SuggestProductTitles(ctx context.Context, arg dbgen.SuggestProductTitlesParams) ([]dbgen.SuggestProductTitlesRow, error)
}

// Search modes reported in ProductListResult.SearchMode (and the API's meta.searchMode).
const (
	SearchModeFTS   = "fts"
	SearchModeFuzzy = "fuzzy"
)

const (
	// minFuzzyQueryRunes is the shortest query that triggers the fuzzy fallback and suggestions.
	minFuzzyQueryRunes = 3
	// suggestionMinScore is the word_similarity floor for "did you mean" suggestions.
	suggestionMinScore = 0.2
	// suggestionLimit caps meta.suggestions.
	suggestionLimit = 5
)

// DefaultPriceBucketEdges are the IDR edges of the price facet (len+1 buckets).
var DefaultPriceBucketEdges = []int64{50000, 100000, 250000, 500000, 1000000, 2500000, 5000000, 10000000}

// Service orchestrates catalog queries, DTO assembly, and caching.
type Service struct {
	queries      QueryProvider
	fuzzy        FuzzyRunner
	cache        *Cache
	defaultPage  int
	defaultLimit int
	maxLimit     int
	priceEdges   []int64
}

// ServiceConfig groups Service dependencies.
type ServiceConfig struct {
	Queries QueryProvider
	// FuzzyRunner enables the pg_trgm fallback when full-text search finds nothing (optional).
	FuzzyRunner FuzzyRunner
	// PriceBucketEdges overrides DefaultPriceBucketEdges (ascending, IDR).
	PriceBucketEdges []int64
	Cache            *Cache
	DefaultPage      int
	DefaultLimit     int
	MaxLimit         int
}

// ListParams captures filters for product listing.
type ListParams struct {
	Query    string
	Category string
	Brand    string
	MinPrice *int64
	MaxPrice *int64
	InStock  *bool
	Sort     string
	Page     int
	Limit    int
	// IncludeFacets requests facet counts (include=facets).
	IncludeFacets bool
}

// ProductListItem represents an entry in list/related responses.
type ProductListItem struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
	Price        int64    `json:"price"`
	CompareAt    *int64   `json:"compareAt,omitempty"`
	InStock      bool     `json:"inStock"`
	Stock        int      `json:"stock"`
	Thumbnail    *string  `json:"thumbnail,omitempty"`
	Badges       []string `json:"badges"`
	CategoryID   *string  `json:"categoryId,omitempty"`
	CategoryName *string  `json:"categoryName,omitempty"`
	BrandID      *string  `json:"brandId,omitempty"`
	BrandName    *string  `json:"brandName,omitempty"`
	Rating       float64  `json:"rating"`
	ReviewCount  int      `json:"reviewCount"`
}

// ProductDetail aggregates the full detail payload.
type ProductDetail struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Slug         string    `json:"slug"`
	Description  string    `json:"description"`
	Price        int64     `json:"price"`
	CompareAt    *int64    `json:"compareAt,omitempty"`
	InStock      bool      `json:"inStock"`
	Stock        int       `json:"stock"`
	Thumbnail    *string   `json:"thumbnail,omitempty"`
	Badges       []string  `json:"badges"`
	Variants     []Variant `json:"variants"`
	Images       []string  `json:"images"`
	Specs        []Spec    `json:"specs"`
	Brand        *Mini     `json:"brand,omitempty"`
	CategoryPath []string  `json:"categoryPath,omitempty"`
}

// Variant describes a product variant.
type Variant struct {
	ID         string         `json:"id"`
	SKU        *string        `json:"sku,omitempty"`
	Price      int64          `json:"price"`
	Stock      int            `json:"stock"`
	Attributes map[string]any `json:"attributes"`
}

// Spec represents a key/value specification entry.
type Spec struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Mini is a minimal representation for brand metadata.
type Mini struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Category represents the public category payload.
type Category struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Slug     string  `json:"slug"`
	ParentID *string `json:"parentId,omitempty"`
}

// Brand represents the public brand payload.
type Brand struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// ProductListResult contains list data and pagination metadata.
type ProductListResult struct {
	Items []ProductListItem
	Total int64
	Page  int
	Limit int
	// SearchMode is "" without a query, otherwise SearchModeFTS or SearchModeFuzzy.
	SearchMode string
	// Facets is set only when ListParams.IncludeFacets is true.
	Facets *Facets
	// Suggestions holds "did you mean" titles when a query matched nothing.
	Suggestions []Suggestion
}

// Facets holds counts per filter dimension. Each dimension honours every other
// active filter but ignores its own, so a client can show alternatives.
type Facets struct {
	Categories []FacetTerm  `json:"categories"`
	Brands     []FacetTerm  `json:"brands"`
	Price      PriceFacet   `json:"price"`
	InStock    InStockFacet `json:"inStock"`
}

// FacetTerm is one category or brand with its matching product count.
type FacetTerm struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int64  `json:"count"`
}

// PriceFacet lists every price bucket, including empty ones.
type PriceFacet struct {
	Buckets []PriceBucket `json:"buckets"`
}

// PriceBucket covers min <= price < max; Max is nil for the open-ended last bucket.
type PriceBucket struct {
	Min   int64  `json:"min"`
	Max   *int64 `json:"max"`
	Count int64  `json:"count"`
}

// InStockFacet counts in-stock and out-of-stock products.
type InStockFacet struct {
	True  int64 `json:"true"`
	False int64 `json:"false"`
}

// Suggestion is a closest-title match offered when a search returns nothing.
type Suggestion struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
}

// NewService constructs a Service instance.
func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Queries == nil {
		return nil, errors.New("catalog: queries provider is required")
	}
	defaultPage := cfg.DefaultPage
	if defaultPage < 1 {
		defaultPage = 1
	}
	defaultLimit := cfg.DefaultLimit
	if defaultLimit < 1 {
		defaultLimit = 20
	}
	maxLimit := cfg.MaxLimit
	if maxLimit < 1 {
		maxLimit = 100
	}
	if defaultLimit > maxLimit {
		defaultLimit = maxLimit
	}
	edges := cfg.PriceBucketEdges
	if len(edges) == 0 {
		edges = DefaultPriceBucketEdges
	}
	for i := 1; i < len(edges); i++ {
		if edges[i] <= edges[i-1] {
			return nil, errors.New("catalog: price bucket edges must be strictly ascending")
		}
	}
	return &Service{
		queries:      cfg.Queries,
		fuzzy:        cfg.FuzzyRunner,
		cache:        cfg.Cache,
		defaultPage:  defaultPage,
		defaultLimit: defaultLimit,
		maxLimit:     maxLimit,
		priceEdges:   append([]int64(nil), edges...),
	}, nil
}

// ParseListParams normalises raw query values into strongly typed filters.
func (s *Service) ParseListParams(values url.Values) (ListParams, error) {
	params := ListParams{
		Page:  s.defaultPage,
		Limit: s.defaultLimit,
	}
	params.Query = strings.TrimSpace(values.Get("q"))
	params.Category = strings.TrimSpace(values.Get("category"))
	params.Brand = strings.TrimSpace(values.Get("brand"))

	if v := strings.TrimSpace(values.Get("page")); v != "" {
		page, err := strconv.Atoi(v)
		if err != nil || page < 1 {
			return params, badRequest("page", "page must be a positive integer", err)
		}
		params.Page = page
	}

	limit := s.defaultLimit
	if v := strings.TrimSpace(values.Get("limit")); v != "" {
		l, err := strconv.Atoi(v)
		if err != nil || l < 1 {
			return params, badRequest("limit", "limit must be a positive integer", err)
		}
		limit = l
	}
	if limit > s.maxLimit {
		limit = s.maxLimit
	}
	params.Limit = limit

	if v := strings.TrimSpace(values.Get("minPrice")); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return params, badRequest("minPrice", "minPrice must be a valid integer", err)
		}
		params.MinPrice = &parsed
	}
	if v := strings.TrimSpace(values.Get("maxPrice")); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return params, badRequest("maxPrice", "maxPrice must be a valid integer", err)
		}
		params.MaxPrice = &parsed
	}
	if params.MinPrice != nil && params.MaxPrice != nil && *params.MinPrice > *params.MaxPrice {
		return params, badRequest("price", "minPrice cannot be greater than maxPrice", fmt.Errorf("invalid price range"))
	}

	if v := strings.TrimSpace(values.Get("inStock")); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return params, badRequest("inStock", "inStock must be true or false", err)
		}
		params.InStock = &b
	}

	params.Sort = normalizeSort(values.Get("sort"))

	for _, raw := range values["include"] {
		for _, token := range strings.Split(raw, ",") {
			switch strings.ToLower(strings.TrimSpace(token)) {
			case "":
			case "facets":
				params.IncludeFacets = true
			default:
				return params, badRequest("include", "include accepts: facets", fmt.Errorf("unknown include %q", token))
			}
		}
	}
	return params, nil
}

// ListBrands returns the list of brands sorted by name.
func (s *Service) ListBrands(ctx context.Context) ([]Brand, error) {
	tenantID := tenantIDFromContext(ctx)
	rows, err := s.queries.ListBrands(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list brands: %w", err)
	}
	result := make([]Brand, 0, len(rows))
	for _, row := range rows {
		result = append(result, Brand{
			ID:   uuidString(row.ID),
			Name: row.Name,
			Slug: row.Slug,
		})
	}
	return result, nil
}

// ListCategories returns all categories with parent linkage.
func (s *Service) ListCategories(ctx context.Context) ([]Category, error) {
	tenantID := tenantIDFromContext(ctx)
	rows, err := s.queries.ListCategories(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	result := make([]Category, 0, len(rows))
	for _, row := range rows {
		cat := Category{
			ID:   uuidString(row.ID),
			Name: row.Name,
			Slug: row.Slug,
		}
		if row.ParentID.Valid {
			parent := uuidString(row.ParentID)
			cat.ParentID = &parent
		}
		result = append(result, cat)
	}
	return result, nil
}

// ListProducts returns filtered product list with pagination metadata.
//
// With a query it first runs full-text search ('simple' config). When that finds
// nothing and the query has at least minFuzzyQueryRunes runes, it retries the same
// count/list/facets with pg_trgm word similarity on the title (via FuzzyRunner).
// When both find nothing, it returns up to suggestionLimit closest titles.
func (s *Service) ListProducts(ctx context.Context, params ListParams) (ProductListResult, error) {
	key, shouldUseCache := s.listCacheKey(ctx, params)
	if shouldUseCache && s.cache != nil {
		var cached cachedList
		ok, err := s.cache.GetJSON(ctx, key, &cached)
		if err == nil && ok {
			return ProductListResult{Items: cached.Items, Total: cached.Total, Page: params.Page, Limit: params.Limit}, nil
		}
	}

	result, err := s.searchPage(ctx, s.queries, params, SearchModeFTS)
	if err != nil {
		return ProductListResult{}, err
	}
	facetsDone := false
	fuzzyEligible := params.Query != "" && utf8.RuneCountInString(params.Query) >= minFuzzyQueryRunes
	if result.Total == 0 && fuzzyEligible && s.fuzzy != nil {
		err := s.fuzzy.RunFuzzy(ctx, func(q QueryProvider) error {
			fuzzyResult, err := s.searchPage(ctx, q, params, SearchModeFuzzy)
			if err != nil {
				return err
			}
			if fuzzyResult.Total == 0 {
				return nil
			}
			if params.IncludeFacets {
				facets, err := s.computeFacets(ctx, q, params, SearchModeFuzzy)
				if err != nil {
					return err
				}
				fuzzyResult.Facets = facets
				facetsDone = true
			}
			result = fuzzyResult
			return nil
		})
		if err != nil {
			return ProductListResult{}, fmt.Errorf("fuzzy search: %w", err)
		}
	}
	if params.IncludeFacets && !facetsDone {
		facets, err := s.computeFacets(ctx, s.queries, params, SearchModeFTS)
		if err != nil {
			return ProductListResult{}, err
		}
		result.Facets = facets
	}
	if result.Total == 0 && fuzzyEligible {
		suggestions, err := s.suggest(ctx, params.Query)
		if err != nil {
			return ProductListResult{}, err
		}
		result.Suggestions = suggestions
	}
	if params.Query == "" {
		result.SearchMode = ""
	}
	if shouldUseCache && s.cache != nil && key != "" {
		_ = s.cache.SetJSON(ctx, key, cachedList{Items: result.Items, Total: result.Total})
	}
	return result, nil
}

// searchFilter carries the filter set shared by the count, list and facet queries.
type searchFilter struct {
	tenantID     pgtype.UUID
	q            pgtype.Text
	mode         string
	categorySlug pgtype.Text
	brandSlug    pgtype.Text
	minPrice     pgtype.Int8
	maxPrice     pgtype.Int8
	inStock      pgtype.Bool
}

func newSearchFilter(ctx context.Context, params ListParams, mode string) searchFilter {
	return searchFilter{
		tenantID:     tenantIDFromContext(ctx),
		q:            optionalStringValue(params.Query),
		mode:         mode,
		categorySlug: optionalStringValue(params.Category),
		brandSlug:    optionalStringValue(params.Brand),
		minPrice:     optionalInt64(params.MinPrice),
		maxPrice:     optionalInt64(params.MaxPrice),
		inStock:      optionalBool(params.InStock),
	}
}

// searchPage runs count then (when anything matched) the page query in one match mode.
func (s *Service) searchPage(ctx context.Context, q QueryProvider, params ListParams, mode string) (ProductListResult, error) {
	f := newSearchFilter(ctx, params, mode)
	total, err := q.CountProductsPublic(ctx, dbgen.CountProductsPublicParams{
		TenantID:     f.tenantID,
		Q:            f.q,
		MatchMode:    f.mode,
		CategorySlug: f.categorySlug,
		BrandSlug:    f.brandSlug,
		MinPrice:     f.minPrice,
		MaxPrice:     f.maxPrice,
		InStock:      f.inStock,
	})
	if err != nil {
		return ProductListResult{}, fmt.Errorf("count products: %w", err)
	}
	result := ProductListResult{Items: []ProductListItem{}, Total: total, Page: params.Page, Limit: params.Limit, SearchMode: mode}
	if total == 0 {
		return result, nil
	}
	offset := int32((params.Page - 1) * params.Limit)
	if offset < 0 {
		offset = 0
	}
	rows, err := q.ListProductsPublic(ctx, dbgen.ListProductsPublicParams{
		TenantID:     f.tenantID,
		Q:            f.q,
		MatchMode:    f.mode,
		CategorySlug: f.categorySlug,
		BrandSlug:    f.brandSlug,
		MinPrice:     f.minPrice,
		MaxPrice:     f.maxPrice,
		InStock:      f.inStock,
		Sort:         params.Sort,
		OffsetValue:  offset,
		LimitValue:   int32(params.Limit),
	})
	if err != nil {
		return ProductListResult{}, fmt.Errorf("list products: %w", err)
	}
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, listItemFromRow(row))
	}
	result.Items = items
	return result, nil
}

func listItemFromRow(row dbgen.ListProductsPublicRow) ProductListItem {
	item := ProductListItem{
		ID:          uuidString(row.ID),
		Title:       row.Title,
		Slug:        row.Slug,
		Price:       row.Price,
		InStock:     row.InStock,
		Stock:       int(row.TotalStock),
		Badges:      row.Badges,
		Rating:      row.Rating,
		ReviewCount: int(row.ReviewCount),
	}
	if row.CompareAt.Valid {
		compareAt := row.CompareAt.Int64
		item.CompareAt = &compareAt
	}
	if row.Thumbnail.Valid {
		thumb := row.Thumbnail.String
		item.Thumbnail = &thumb
	}
	// Category and brand come from LEFT JOINs, so both may be absent.
	if row.CategoryID.Valid {
		categoryID := uuidString(row.CategoryID)
		item.CategoryID = &categoryID
	}
	if row.CategoryName.Valid {
		categoryName := row.CategoryName.String
		item.CategoryName = &categoryName
	}
	if row.BrandID.Valid {
		brandID := uuidString(row.BrandID)
		item.BrandID = &brandID
	}
	if row.BrandName.Valid {
		brandName := row.BrandName.String
		item.BrandName = &brandName
	}
	return item
}

// computeFacets runs the four facet queries; each omits its own dimension's filter.
func (s *Service) computeFacets(ctx context.Context, q QueryProvider, params ListParams, mode string) (*Facets, error) {
	f := newSearchFilter(ctx, params, mode)
	facets := &Facets{Categories: []FacetTerm{}, Brands: []FacetTerm{}}

	catRows, err := q.FacetCategoriesPublic(ctx, dbgen.FacetCategoriesPublicParams{
		TenantID:  f.tenantID,
		Q:         f.q,
		MatchMode: f.mode,
		BrandSlug: f.brandSlug,
		MinPrice:  f.minPrice,
		MaxPrice:  f.maxPrice,
		InStock:   f.inStock,
	})
	if err != nil {
		return nil, fmt.Errorf("facet categories: %w", err)
	}
	for _, row := range catRows {
		facets.Categories = append(facets.Categories, FacetTerm{ID: uuidString(row.ID), Name: row.Name, Slug: row.Slug, Count: row.Count})
	}

	brandRows, err := q.FacetBrandsPublic(ctx, dbgen.FacetBrandsPublicParams{
		TenantID:     f.tenantID,
		Q:            f.q,
		MatchMode:    f.mode,
		CategorySlug: f.categorySlug,
		MinPrice:     f.minPrice,
		MaxPrice:     f.maxPrice,
		InStock:      f.inStock,
	})
	if err != nil {
		return nil, fmt.Errorf("facet brands: %w", err)
	}
	for _, row := range brandRows {
		facets.Brands = append(facets.Brands, FacetTerm{ID: uuidString(row.ID), Name: row.Name, Slug: row.Slug, Count: row.Count})
	}

	priceRows, err := q.FacetPriceBucketsPublic(ctx, dbgen.FacetPriceBucketsPublicParams{
		Edges:        s.priceEdges,
		TenantID:     f.tenantID,
		Q:            f.q,
		MatchMode:    f.mode,
		CategorySlug: f.categorySlug,
		BrandSlug:    f.brandSlug,
		InStock:      f.inStock,
	})
	if err != nil {
		return nil, fmt.Errorf("facet price buckets: %w", err)
	}
	facets.Price.Buckets = s.emptyPriceBuckets()
	for _, row := range priceRows {
		idx := int(row.Bucket)
		if idx < 0 || idx >= len(facets.Price.Buckets) {
			continue
		}
		facets.Price.Buckets[idx].Count += row.Count
	}

	stockRows, err := q.FacetInStockPublic(ctx, dbgen.FacetInStockPublicParams{
		TenantID:     f.tenantID,
		Q:            f.q,
		MatchMode:    f.mode,
		CategorySlug: f.categorySlug,
		BrandSlug:    f.brandSlug,
		MinPrice:     f.minPrice,
		MaxPrice:     f.maxPrice,
	})
	if err != nil {
		return nil, fmt.Errorf("facet in stock: %w", err)
	}
	for _, row := range stockRows {
		if row.InStock {
			facets.InStock.True += row.Count
		} else {
			facets.InStock.False += row.Count
		}
	}
	return facets, nil
}

// emptyPriceBuckets returns len(edges)+1 zero-count buckets: [0,e0), [e0,e1), ..., [eN,inf).
func (s *Service) emptyPriceBuckets() []PriceBucket {
	buckets := make([]PriceBucket, 0, len(s.priceEdges)+1)
	var lower int64
	for _, edge := range s.priceEdges {
		upper := edge
		buckets = append(buckets, PriceBucket{Min: lower, Max: &upper})
		lower = edge
	}
	return append(buckets, PriceBucket{Min: lower})
}

func (s *Service) suggest(ctx context.Context, query string) ([]Suggestion, error) {
	rows, err := s.queries.SuggestProductTitles(ctx, dbgen.SuggestProductTitlesParams{
		Q:          query,
		TenantID:   tenantIDFromContext(ctx),
		MinScore:   suggestionMinScore,
		LimitValue: suggestionLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("suggest products: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	suggestions := make([]Suggestion, 0, len(rows))
	for _, row := range rows {
		suggestions = append(suggestions, Suggestion{Title: row.Title, Slug: row.Slug})
	}
	return suggestions, nil
}

// GetProductDetail returns product detail, variants, images, specs, and metadata.
func (s *Service) GetProductDetail(ctx context.Context, slug string) (ProductDetail, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ProductDetail{}, badRequest("slug", "slug is required", nil)
	}
	var cacheKey string
	if s.cache != nil {
		cacheKey = s.cache.ProductDetailKeyForTenant(tenantCacheKey(ctx), slug)
		var cached ProductDetail
		ok, err := s.cache.GetJSON(ctx, cacheKey, &cached)
		if err == nil && ok {
			return cached, nil
		}
	}
	product, err := s.queries.GetProductBySlug(ctx, dbgen.GetProductBySlugParams{Slug: slug, TenantID: tenantIDFromContext(ctx)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductDetail{}, &common.AppError{Code: "NOT_FOUND", Message: "product not found", HTTPStatus: http.StatusNotFound, Err: err}
		}
		return ProductDetail{}, fmt.Errorf("get product by slug: %w", err)
	}
	detail := ProductDetail{
		ID:      uuidString(product.ID),
		Title:   product.Title,
		Slug:    product.Slug,
		Price:   product.Price,
		InStock: product.InStock,
		Stock:   int(product.TotalStock),
		Badges:  product.Badges,
	}
	if product.Description.Valid {
		detail.Description = product.Description.String
	}
	if product.CompareAt.Valid {
		compareAt := product.CompareAt.Int64
		detail.CompareAt = &compareAt
	}
	if product.Thumbnail.Valid {
		thumb := product.Thumbnail.String
		detail.Thumbnail = &thumb
	}
	if product.BrandID.Valid {
		brand, err := s.queries.GetBrandByID(ctx, dbgen.GetBrandByIDParams{ID: product.BrandID, TenantID: tenantIDFromContext(ctx)})
		if err == nil {
			detail.Brand = &Mini{ID: uuidString(brand.ID), Name: brand.Name, Slug: brand.Slug}
		}
	}
	if product.CategoryID.Valid {
		path, err := s.categoryPath(ctx, product.CategoryID)
		if err == nil {
			detail.CategoryPath = path
		}
	}
	variants, err := s.queries.ListVariantsByProduct(ctx, product.ID)
	if err != nil {
		return ProductDetail{}, fmt.Errorf("list variants: %w", err)
	}
	detail.Variants = make([]Variant, 0, len(variants))
	for _, row := range variants {
		attrs := map[string]any{}
		if len(row.Attributes) > 0 {
			if err := json.Unmarshal(row.Attributes, &attrs); err != nil {
				attrs = map[string]any{}
			}
		}
		variant := Variant{
			ID:         uuidString(row.ID),
			Price:      row.Price,
			Stock:      int(row.Stock),
			Attributes: attrs,
		}
		if row.Sku.Valid {
			sku := row.Sku.String
			variant.SKU = &sku
		}
		detail.Variants = append(detail.Variants, variant)
	}
	images, err := s.queries.ListImagesByProduct(ctx, product.ID)
	if err != nil {
		return ProductDetail{}, fmt.Errorf("list images: %w", err)
	}
	detail.Images = make([]string, 0, len(images))
	for _, row := range images {
		detail.Images = append(detail.Images, row.Url)
	}
	specs, err := s.queries.ListSpecsByProduct(ctx, product.ID)
	if err != nil {
		return ProductDetail{}, fmt.Errorf("list specs: %w", err)
	}
	detail.Specs = make([]Spec, 0, len(specs))
	for _, row := range specs {
		detail.Specs = append(detail.Specs, Spec{Key: row.Key, Value: row.Value})
	}
	if s.cache != nil && cacheKey != "" {
		_ = s.cache.SetJSON(ctx, cacheKey, detail)
	}
	return detail, nil
}

// ListRelatedProducts fetches related products from the same category.
func (s *Service) ListRelatedProducts(ctx context.Context, slug string) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)
	product, err := s.queries.GetProductBySlug(ctx, dbgen.GetProductBySlugParams{Slug: slug, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &common.AppError{Code: "NOT_FOUND", Message: "product not found", HTTPStatus: http.StatusNotFound, Err: err}
		}
		return nil, fmt.Errorf("get product by slug: %w", err)
	}
	if !product.CategoryID.Valid {
		return []ProductListItem{}, nil
	}
	rows, err := s.queries.ListRelatedByCategory(ctx, dbgen.ListRelatedByCategoryParams{CategoryID: product.CategoryID, Slug: slug, TenantID: tenantID})
	if err != nil {
		return nil, fmt.Errorf("list related products: %w", err)
	}
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		item := ProductListItem{
			ID:      uuidString(row.ID),
			Title:   row.Title,
			Slug:    row.Slug,
			Price:   row.Price,
			InStock: row.InStock,
			Badges:  row.Badges,
		}
		if row.CompareAt.Valid {
			compareAt := row.CompareAt.Int64
			item.CompareAt = &compareAt
		}
		if row.Thumbnail.Valid {
			thumb := row.Thumbnail.String
			item.Thumbnail = &thumb
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) categoryPath(ctx context.Context, id pgtype.UUID) ([]string, error) {
	var path []string
	if !id.Valid {
		return path, nil
	}
	seen := make(map[string]struct{})
	current := id
	for current.Valid {
		cat, err := s.queries.GetCategoryByID(ctx, dbgen.GetCategoryByIDParams{ID: current, TenantID: tenantIDFromContext(ctx)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			return nil, err
		}
		slug := strings.TrimSpace(cat.Slug)
		if slug != "" {
			path = append([]string{slug}, path...)
		}
		key := uuidString(cat.ID)
		if _, ok := seen[key]; ok {
			break
		}
		seen[key] = struct{}{}
		current = cat.ParentID
	}
	return path, nil
}

type cachedList struct {
	Items []ProductListItem `json:"items"`
	Total int64             `json:"total"`
}

func (s *Service) listCacheKey(ctx context.Context, params ListParams) (string, bool) {
	if s.cache == nil {
		return "", false
	}
	if params.Page != s.defaultPage {
		return "", false
	}
	if params.Limit != s.defaultLimit {
		return "", false
	}
	if params.Query != "" || params.Category != "" || params.Brand != "" || params.MinPrice != nil || params.MaxPrice != nil || params.InStock != nil || params.Sort != "" || params.IncludeFacets {
		return "", false
	}
	return s.cache.ProductListKeyForTenant(tenantCacheKey(ctx)), true
}

func optionalStringValue(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: trimmed, Valid: true}
}

func optionalInt64(ptr *int64) pgtype.Int8 {
	if ptr == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *ptr, Valid: true}
}

func optionalBool(ptr *bool) pgtype.Bool {
	if ptr == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *ptr, Valid: true}
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		return true, nil
	case "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean: %s", value)
	}
}

func normalizeSort(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "price:asc", "price:desc", "title:asc", "title:desc", "popularity:desc", "rating:desc":
		return s
	case "popularity":
		return "popularity:desc"
	case "rating":
		return "rating:desc"
	default:
		return ""
	}
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

func tenantIDFromContext(ctx context.Context) pgtype.UUID {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

func tenantCacheKey(ctx context.Context) string {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return "unscoped"
	}
	return id
}

func badRequest(field, message string, err error) *common.AppError {
	return &common.AppError{
		Code:       "BAD_REQUEST",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
		Err:        err,
		Details: map[string]any{
			"field": field,
		},
	}
}
