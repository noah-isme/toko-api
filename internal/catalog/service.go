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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type queryProvider interface {
	ListBrands(ctx context.Context) ([]dbgen.ListBrandsRow, error)
	GetBrandByID(ctx context.Context, id pgtype.UUID) (dbgen.GetBrandByIDRow, error)
	ListCategories(ctx context.Context) ([]dbgen.ListCategoriesRow, error)
	GetCategoryByID(ctx context.Context, id pgtype.UUID) (dbgen.GetCategoryByIDRow, error)
	CountProductsPublic(ctx context.Context, arg dbgen.CountProductsPublicParams) (int64, error)
	ListProductsPublic(ctx context.Context, arg dbgen.ListProductsPublicParams) ([]dbgen.ListProductsPublicRow, error)
	GetProductBySlug(ctx context.Context, slug string) (dbgen.GetProductBySlugRow, error)
	ListVariantsByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductVariant, error)
	ListImagesByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductImage, error)
	ListSpecsByProduct(ctx context.Context, productID pgtype.UUID) ([]dbgen.ProductSpec, error)
	ListRelatedByCategory(ctx context.Context, arg dbgen.ListRelatedByCategoryParams) ([]dbgen.ListRelatedByCategoryRow, error)
}

// Service orchestrates catalog queries, DTO assembly, and caching.
type Service struct {
	queries      queryProvider
	cache        *Cache
	defaultPage  int
	defaultLimit int
	maxLimit     int
}

// ServiceConfig groups Service dependencies.
type ServiceConfig struct {
	Queries      queryProvider
	Cache        *Cache
	DefaultPage  int
	DefaultLimit int
	MaxLimit     int
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
}

// ProductListItem represents an entry in list/related responses.
type ProductListItem struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Slug      string   `json:"slug"`
	Price     int64    `json:"price"`
	CompareAt *int64   `json:"compareAt,omitempty"`
	InStock   bool     `json:"inStock"`
	Thumbnail *string  `json:"thumbnail,omitempty"`
	Badges    []string `json:"badges"`
}

// ProductDetail aggregates the full detail payload.
type ProductDetail struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Slug         string    `json:"slug"`
	Price        int64     `json:"price"`
	CompareAt    *int64    `json:"compareAt,omitempty"`
	InStock      bool      `json:"inStock"`
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
	return &Service{
		queries:      cfg.Queries,
		cache:        cfg.Cache,
		defaultPage:  defaultPage,
		defaultLimit: defaultLimit,
		maxLimit:     maxLimit,
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
	return params, nil
}

// ListBrands returns the list of brands sorted by name.
func (s *Service) ListBrands(ctx context.Context) ([]Brand, error) {
	rows, err := s.queries.ListBrands(ctx)
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
	rows, err := s.queries.ListCategories(ctx)
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
func (s *Service) ListProducts(ctx context.Context, params ListParams) (ProductListResult, error) {
	key, shouldUseCache := s.listCacheKey(params)
	if shouldUseCache && s.cache != nil {
		var cached cachedList
		ok, err := s.cache.GetJSON(ctx, key, &cached)
		if err == nil && ok {
			return ProductListResult{Items: cached.Items, Total: cached.Total, Page: params.Page, Limit: params.Limit}, nil
		}
	}

	countParams := dbgen.CountProductsPublicParams{
		Q:            optionalStringValue(params.Query),
		CategorySlug: optionalStringValue(params.Category),
		BrandSlug:    optionalStringValue(params.Brand),
		MinPrice:     optionalInt64(params.MinPrice),
		MaxPrice:     optionalInt64(params.MaxPrice),
		InStock:      optionalBool(params.InStock),
	}
	total, err := s.queries.CountProductsPublic(ctx, countParams)
	if err != nil {
		return ProductListResult{}, fmt.Errorf("count products: %w", err)
	}
	offset := int32((params.Page - 1) * params.Limit)
	if offset < 0 {
		offset = 0
	}
	listParams := dbgen.ListProductsPublicParams{
		Q:            countParams.Q,
		CategorySlug: countParams.CategorySlug,
		BrandSlug:    countParams.BrandSlug,
		MinPrice:     countParams.MinPrice,
		MaxPrice:     countParams.MaxPrice,
		InStock:      countParams.InStock,
		Sort:         optionalStringValue(params.Sort),
		OffsetValue:  offset,
		LimitValue:   int32(params.Limit),
	}
	rows, err := s.queries.ListProductsPublic(ctx, listParams)
	if err != nil {
		return ProductListResult{}, fmt.Errorf("list products: %w", err)
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
	result := ProductListResult{Items: items, Total: total, Page: params.Page, Limit: params.Limit}
	if shouldUseCache && s.cache != nil {
		_ = s.cache.SetJSON(ctx, key, cachedList{Items: items, Total: total})
	}
	return result, nil
}

// GetProductDetail returns product detail, variants, images, specs, and metadata.
func (s *Service) GetProductDetail(ctx context.Context, slug string) (ProductDetail, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return ProductDetail{}, badRequest("slug", "slug is required", nil)
	}
	cacheKey := detailCacheKey(slug)
	if s.cache != nil {
		var cached ProductDetail
		ok, err := s.cache.GetJSON(ctx, cacheKey, &cached)
		if err == nil && ok {
			return cached, nil
		}
	}
	product, err := s.queries.GetProductBySlug(ctx, slug)
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
		Badges:  product.Badges,
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
		brand, err := s.queries.GetBrandByID(ctx, product.BrandID)
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
	if s.cache != nil {
		_ = s.cache.SetJSON(ctx, cacheKey, detail)
	}
	return detail, nil
}

// ListRelatedProducts fetches related products from the same category.
func (s *Service) ListRelatedProducts(ctx context.Context, slug string) ([]ProductListItem, error) {
	product, err := s.queries.GetProductBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &common.AppError{Code: "NOT_FOUND", Message: "product not found", HTTPStatus: http.StatusNotFound, Err: err}
		}
		return nil, fmt.Errorf("get product by slug: %w", err)
	}
	if !product.CategoryID.Valid {
		return []ProductListItem{}, nil
	}
	rows, err := s.queries.ListRelatedByCategory(ctx, dbgen.ListRelatedByCategoryParams{CategoryID: product.CategoryID, Slug: slug})
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
		cat, err := s.queries.GetCategoryByID(ctx, current)
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

func (s *Service) listCacheKey(params ListParams) (string, bool) {
	if params.Page != s.defaultPage {
		return "", false
	}
	if params.Limit != s.defaultLimit {
		return "", false
	}
	if params.Query != "" || params.Category != "" || params.Brand != "" || params.MinPrice != nil || params.MaxPrice != nil || params.InStock != nil || params.Sort != "" {
		return "", false
	}
	return "catalog:products:list:popular", true
}

func detailCacheKey(slug string) string {
	return "catalog:products:detail:" + slug
}

func optionalStringValue(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func optionalInt64(ptr *int64) any {
	if ptr == nil {
		return nil
	}
	return *ptr
}

func optionalBool(ptr *bool) any {
	if ptr == nil {
		return nil
	}
	return *ptr
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
	case "price:asc", "price:desc", "title:asc", "title:desc":
		return s
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
