package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// CatalogHandler serves admin product, category, and brand management, plus
// the catalog operations (bulk updates, CSV export/import).
type CatalogHandler struct {
	Q     dbgen.Querier
	Cache *catalog.Cache
	// Pool runs the transactional catalog operations (bulk, import). Optional:
	// without it those endpoints answer 500 after validating the request.
	Pool TxBeginner
	// Events publishes product.* domain events. Optional.
	Events *events.Bus
	// AuditEnabled turns on the explicit per-operation audit rows written by
	// bulk and import (never sampled).
	AuditEnabled bool
	// ExportBatchSize is the number of products fetched per export page
	// (default 500). Tests lower it to exercise page boundaries.
	ExportBatchSize int
}

type productDTO struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
	Status       string   `json:"status"`
	Price        int64    `json:"price"`
	CompareAt    *int64   `json:"compareAt,omitempty"`
	InStock      bool     `json:"inStock"`
	Stock        int32    `json:"stock"`
	Thumbnail    *string  `json:"thumbnail,omitempty"`
	Badges       []string `json:"badges"`
	Description  *string  `json:"description,omitempty"`
	CategoryID   *string  `json:"categoryId,omitempty"`
	CategoryName *string  `json:"categoryName,omitempty"`
	BrandID      *string  `json:"brandId,omitempty"`
	BrandName    *string  `json:"brandName,omitempty"`
	VariantCount int32    `json:"variantCount"`
	SKU          *string  `json:"sku,omitempty"`
	StockStatus  string   `json:"stockStatus"`
	// LowStockThreshold is the product override; null means the tenant default.
	LowStockThreshold *int32 `json:"lowStockThreshold"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

type productImageDTO struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	SortOrder int32  `json:"sortOrder"`
}

type productSpecDTO struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type productVariantDTO struct {
	ID         string          `json:"id"`
	SKU        *string         `json:"sku,omitempty"`
	Price      int64           `json:"price"`
	Stock      int32           `json:"stock"`
	Attributes json.RawMessage `json:"attributes"`
}

type productDetailDTO struct {
	productDTO
	Images   []productImageDTO   `json:"images"`
	Specs    []productSpecDTO    `json:"specs"`
	Variants []productVariantDTO `json:"variants"`
}

type variantPayload struct {
	ID         *string         `json:"id"`
	SKU        *string         `json:"sku"`
	Price      *int64          `json:"price"`
	Stock      *int32          `json:"stock"`
	Attributes json.RawMessage `json:"attributes"`
}

type productPayload struct {
	Title       *string          `json:"title"`
	Slug        *string          `json:"slug"`
	Price       *int64           `json:"price"`
	CompareAt   *int64           `json:"compareAt"`
	InStock     *bool            `json:"inStock"`
	Thumbnail   *string          `json:"thumbnail"`
	Badges      []string         `json:"badges"`
	Description *string          `json:"description"`
	CategoryID  *string          `json:"categoryId"`
	BrandID     *string          `json:"brandId"`
	Images      []string         `json:"images"`
	Specs       []productSpecDTO `json:"specs"`
	Variants    []variantPayload `json:"variants"`
	Status      *string          `json:"status"`
	// LowStockThreshold: present-and-null clears the override on PATCH.
	LowStockThreshold *int64 `json:"lowStockThreshold"`
	// fieldsPresent records which optional keys the client actually sent so
	// partial updates can distinguish "clear this" from "leave unchanged".
	fieldsPresent map[string]bool
}

func decodeProductPayload(r *http.Request) (productPayload, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return productPayload{}, err
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return productPayload{}, err
	}
	var payload productPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		return productPayload{}, err
	}
	payload.fieldsPresent = make(map[string]bool, len(raw))
	for key := range raw {
		payload.fieldsPresent[key] = true
	}
	return payload, nil
}

func (p productPayload) has(field string) bool {
	if p.fieldsPresent == nil {
		return false
	}
	return p.fieldsPresent[field]
}

// ListProducts handles GET /api/v1/admin/products.
func (h *CatalogHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	page, limit, offset := parseListQuery(r)
	filters, sort, err := parseProductListFilters(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	threshold, err := catalog.ResolveTenantLowStockThreshold(ctx, h.Q, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load inventory settings", nil)
		return
	}
	total, err := h.Q.AdminCountProducts(ctx, dbgen.AdminCountProductsParams{
		TenantID:         tenantID,
		Search:           filters.Search,
		CategorySlug:     filters.Category,
		BrandSlug:        filters.Brand,
		InStock:          filters.InStock,
		Status:           filters.Status,
		StockStatus:      filters.StockStatus,
		DefaultThreshold: threshold,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count products", nil)
		return
	}
	rows, err := h.Q.AdminListProducts(ctx, dbgen.AdminListProductsParams{
		TenantID:         tenantID,
		Search:           filters.Search,
		CategorySlug:     filters.Category,
		BrandSlug:        filters.Brand,
		InStock:          filters.InStock,
		Status:           filters.Status,
		StockStatus:      filters.StockStatus,
		DefaultThreshold: threshold,
		Sort:             sort,
		LimitValue:       int32(limit),
		OffsetValue:      int32(offset),
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list products", nil)
		return
	}
	items := make([]productDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, productDTO{
			ID:           uuidString(row.ID),
			Title:        row.Title,
			Slug:         row.Slug,
			Status:       row.Status,
			Price:        row.Price,
			CompareAt:    nullableInt64(row.CompareAt),
			InStock:      row.InStock,
			Stock:        row.TotalStock,
			Thumbnail:    nullableString(row.Thumbnail),
			Badges:       normalizeBadges(row.Badges),
			Description:  nullableString(row.Description),
			CategoryID:   nullableUUID(row.CategoryID),
			CategoryName: nullableString(row.CategoryName),
			BrandID:      nullableUUID(row.BrandID),
			BrandName:    nullableString(row.BrandName),
			VariantCount: row.VariantCount,
			SKU:          nullableString(row.PrimarySku),
			StockStatus:  row.StockStatus,

			LowStockThreshold: nullableInt32(row.LowStockThreshold),
			CreatedAt:         timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:         timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		})
	}
	writePaginated(w, items, page, limit, total)
}

// GetProduct handles GET /api/v1/admin/products/{id}.
func (h *CatalogHandler) GetProduct(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	ctx := r.Context()
	id, err := h.resolveProductID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	row, err := h.Q.AdminGetProduct(ctx, dbgen.AdminGetProductParams{ID: id, TenantID: tenantIDFromContext(ctx)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load product", nil)
		return
	}
	images, err := h.Q.ListImagesByProduct(ctx, id)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load images", nil)
		return
	}
	specs, err := h.Q.ListSpecsByProduct(ctx, id)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load specs", nil)
		return
	}
	variants, err := h.Q.ListVariantsByProduct(ctx, id)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load variants", nil)
		return
	}
	threshold, err := catalog.ResolveTenantLowStockThreshold(ctx, h.Q, tenantIDFromContext(ctx))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load inventory settings", nil)
		return
	}

	detail := productDetailDTO{
		productDTO: productDTO{
			ID:           uuidString(row.ID),
			Title:        row.Title,
			Slug:         row.Slug,
			Status:       row.Status,
			Price:        row.Price,
			CompareAt:    nullableInt64(row.CompareAt),
			InStock:      row.InStock,
			Stock:        row.TotalStock,
			Thumbnail:    nullableString(row.Thumbnail),
			Badges:       normalizeBadges(row.Badges),
			Description:  nullableString(row.Description),
			CategoryID:   nullableUUID(row.CategoryID),
			CategoryName: nullableString(row.CategoryName),
			BrandID:      nullableUUID(row.BrandID),
			BrandName:    nullableString(row.BrandName),
			VariantCount: int32(len(variants)),
			StockStatus: catalog.StockStatus(int32(len(variants)), row.TotalStock,
				catalog.EffectiveThreshold(row.LowStockThreshold, threshold), row.InStock),
			LowStockThreshold: nullableInt32(row.LowStockThreshold),
			CreatedAt:         timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:         timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		},
		Images:   make([]productImageDTO, 0, len(images)),
		Specs:    make([]productSpecDTO, 0, len(specs)),
		Variants: make([]productVariantDTO, 0, len(variants)),
	}
	for _, img := range images {
		detail.Images = append(detail.Images, productImageDTO{
			ID:        uuidString(img.ID),
			URL:       img.Url,
			SortOrder: img.SortOrder,
		})
	}
	for _, spec := range specs {
		detail.Specs = append(detail.Specs, productSpecDTO{Key: spec.Key, Value: spec.Value})
	}
	for _, variant := range variants {
		detail.Variants = append(detail.Variants, productVariantDTO{
			ID:         uuidString(variant.ID),
			SKU:        nullableString(variant.Sku),
			Price:      variant.Price,
			Stock:      variant.Stock,
			Attributes: rawJSON(variant.Attributes),
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": detail})
}

// CreateProduct handles POST /api/v1/admin/products.
func (h *CatalogHandler) CreateProduct(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	payload, err := decodeProductPayload(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	title := strings.TrimSpace(derefString(payload.Title))
	if title == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "title is required", nil)
		return
	}
	if payload.Price == nil || *payload.Price < 0 {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "price must be zero or greater", nil)
		return
	}
	slug := slugify(derefString(payload.Slug))
	if slug == "" {
		slug = slugify(title)
	}
	if slug == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "slug could not be derived from title", nil)
		return
	}
	brandID, err := optionalUUID(payload.BrandID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid brandId", nil)
		return
	}
	categoryID, err := optionalUUID(payload.CategoryID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid categoryId", nil)
		return
	}
	inStock := true
	if payload.InStock != nil {
		inStock = *payload.InStock
	}
	status := productStatusPublished
	if payload.Status != nil {
		parsed, err := parseProductStatus(*payload.Status)
		if err != nil || !parsed.Valid {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "status must be draft, published or archived", nil)
			return
		}
		status = parsed.String
	}
	threshold, err := payloadLowStockThreshold(payload)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	id, err := h.Q.AdminCreateProduct(ctx, dbgen.AdminCreateProductParams{
		Title:       title,
		Slug:        slug,
		BrandID:     brandID,
		CategoryID:  categoryID,
		Price:       *payload.Price,
		CompareAt:   optionalInt64(payload.CompareAt),
		InStock:     inStock,
		Thumbnail:   optionalText(payload.Thumbnail),
		Badges:      payload.Badges,
		Description: optionalText(payload.Description),
		TenantID:    tenantID,
		Status:      status,

		LowStockThreshold: threshold,
	})
	if err != nil {
		if isUniqueViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "slug already exists", nil)
			return
		}
		if isForeignKeyViolation(err) {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown brand or category", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create product", nil)
		return
	}
	if err := h.writeChildRows(ctx, id, payload, true); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "product created but child rows failed: "+err.Error(), nil)
		return
	}
	h.invalidate(ctx, slug)
	common.JSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": uuidString(id), "slug": slug}})
}

// UpdateProduct handles PATCH /api/v1/admin/products/{id}.
func (h *CatalogHandler) UpdateProduct(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	id, err := h.resolveProductID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	existing, err := h.Q.AdminGetProduct(ctx, dbgen.AdminGetProductParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load product", nil)
		return
	}
	payload, err := decodeProductPayload(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	brandID, err := optionalUUID(payload.BrandID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid brandId", nil)
		return
	}
	categoryID, err := optionalUUID(payload.CategoryID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid categoryId", nil)
		return
	}
	var status pgtype.Text
	if payload.has("status") {
		if payload.Status == nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "status must be draft, published or archived", nil)
			return
		}
		status, err = parseProductStatus(*payload.Status)
		if err != nil || !status.Valid {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "status must be draft, published or archived", nil)
			return
		}
	}
	threshold, err := payloadLowStockThreshold(payload)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if payload.Price != nil && *payload.Price < 0 {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "price must be zero or greater", nil)
		return
	}
	var slug pgtype.Text
	if payload.has("slug") {
		normalized := slugify(derefString(payload.Slug))
		if normalized == "" {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "slug cannot be empty", nil)
			return
		}
		slug = pgtype.Text{String: normalized, Valid: true}
	}
	params := dbgen.AdminUpdateProductParams{
		Title:          optionalText(payload.Title),
		Slug:           slug,
		SetBrand:       payload.has("brandId"),
		BrandID:        brandID,
		SetCategory:    payload.has("categoryId"),
		CategoryID:     categoryID,
		Price:          optionalInt64(payload.Price),
		SetCompareAt:   payload.has("compareAt"),
		CompareAt:      optionalInt64(payload.CompareAt),
		InStock:        optionalBool(payload.InStock),
		SetThumbnail:   payload.has("thumbnail"),
		Thumbnail:      optionalText(payload.Thumbnail),
		SetBadges:      payload.has("badges"),
		Badges:         payload.Badges,
		SetDescription: payload.has("description"),
		Description:    optionalText(payload.Description),
		Status:         status,
		ID:             id,
		TenantID:       tenantID,

		SetLowStockThreshold: payload.has("lowStockThreshold"),
		LowStockThreshold:    threshold,
	}
	var oldVariants []dbgen.ProductVariant
	if len(payload.Variants) > 0 {
		if oldVariants, err = h.Q.ListVariantsByProduct(ctx, id); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load variants", nil)
			return
		}
	}
	if _, err := h.Q.AdminUpdateProduct(ctx, params); err != nil {
		if isUniqueViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "slug already exists", nil)
			return
		}
		if isForeignKeyViolation(err) {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown brand or category", nil)
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update product", nil)
		return
	}
	if err := h.writeChildRows(ctx, id, payload, false); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "product updated but child rows failed: "+err.Error(), nil)
		return
	}
	newSlug := existing.Slug
	if slug.Valid {
		newSlug = slug.String
	}
	changes := productUpdateChanges(existing, params, newSlug)
	if len(oldVariants) > 0 {
		newVariants, err := h.Q.ListVariantsByProduct(ctx, id)
		if err == nil {
			changes = append(changes, variantChanges(id, newSlug, oldVariants, newVariants)...)
		}
	}
	h.afterProductChanges(ctx, catalog.ChangeSourceSingle, changes, existing.Slug, newSlug)
	w.WriteHeader(http.StatusNoContent)
}

// DeleteProduct handles DELETE /api/v1/admin/products/{id}.
func (h *CatalogHandler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	id, err := h.resolveProductID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	existing, err := h.Q.AdminGetProduct(ctx, dbgen.AdminGetProductParams{ID: id, TenantID: tenantID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load product", nil)
		return
	}
	affected, err := h.Q.AdminDeleteProduct(ctx, dbgen.AdminDeleteProductParams{ID: id, TenantID: tenantID})
	if err != nil {
		if isForeignKeyViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "product is referenced by existing orders", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete product", nil)
		return
	}
	if affected == 0 {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	h.invalidate(ctx, existing.Slug)
	w.WriteHeader(http.StatusNoContent)
}

type stockPayload struct {
	Stock   *int32 `json:"stock"`
	InStock *bool  `json:"inStock"`
}

// UpdateProductStock handles PATCH /api/v1/admin/products/{id}/stock. It writes
// the primary variant's stock (creating one when the product has none) and keeps
// the denormalized products.in_stock flag consistent.
func (h *CatalogHandler) UpdateProductStock(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	id, err := h.resolveProductID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	product, err := h.Q.AdminGetProduct(ctx, dbgen.AdminGetProductParams{ID: id, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load product", nil)
		return
	}
	var payload stockPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if payload.Stock == nil && payload.InStock == nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "stock or inStock is required", nil)
		return
	}
	var changes []catalog.ProductChange
	if payload.Stock != nil {
		if *payload.Stock < 0 {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "stock cannot be negative", nil)
			return
		}
		variant, err := h.Q.AdminGetPrimaryVariant(ctx, dbgen.AdminGetPrimaryVariantParams{ProductID: id, TenantID: tenantID})
		switch {
		case err == nil:
			changes = append(changes, catalog.ProductChange{
				ProductID: id, VariantID: variant.ID, Slug: product.Slug, SKU: variant.Sku.String,
				Field: catalog.FieldStock, Old: variant.Stock, New: *payload.Stock,
			})
			if _, err := h.Q.AdminUpdateProductVariant(ctx, dbgen.AdminUpdateProductVariantParams{
				Stock:     pgtype.Int4{Int32: *payload.Stock, Valid: true},
				ID:        variant.ID,
				ProductID: id,
				TenantID:  tenantID,
			}); err != nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update stock", nil)
				return
			}
		case errors.Is(err, pgx.ErrNoRows):
			variantID, err := h.Q.AdminInsertProductVariant(ctx, dbgen.AdminInsertProductVariantParams{
				ProductID:  id,
				Price:      product.Price,
				Stock:      *payload.Stock,
				Attributes: []byte(`{}`),
				TenantID:   tenantID,
			})
			if err != nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create variant for stock", nil)
				return
			}
			changes = append(changes, catalog.ProductChange{
				ProductID: id, VariantID: variantID, Slug: product.Slug,
				Field: catalog.FieldStock, Old: int32(0), New: *payload.Stock,
			})
		default:
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load variant", nil)
			return
		}
	}

	inStock := product.InStock
	switch {
	case payload.InStock != nil:
		inStock = *payload.InStock
	case payload.Stock != nil:
		inStock = *payload.Stock > 0
	}
	if err := h.Q.AdminSetProductStockFlag(ctx, dbgen.AdminSetProductStockFlagParams{InStock: inStock, ID: id, TenantID: tenantID}); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update stock flag", nil)
		return
	}
	h.afterProductChanges(ctx, catalog.ChangeSourceSingle, changes, product.Slug)
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id":      uuidString(id),
		"inStock": inStock,
		"stock":   payload.Stock,
	}})
}

func (h *CatalogHandler) resolveProductID(ctx context.Context, raw string) (pgtype.UUID, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return pgtype.UUID{}, errors.New("missing product identifier")
	}
	if id, err := parsePGUUID(trimmed); err == nil {
		return id, nil
	}
	return h.Q.AdminGetProductIDBySlug(ctx, dbgen.AdminGetProductIDBySlugParams{Slug: trimmed, TenantID: tenantIDFromContext(ctx)})
}

// writeChildRows replaces images/specs and upserts variants when the payload
// includes them. On create the caller passes replaceAll so absent collections
// still produce a consistent (empty) child set.
func (h *CatalogHandler) writeChildRows(ctx context.Context, id pgtype.UUID, payload productPayload, isCreate bool) error {
	tenantID := tenantIDFromContext(ctx)
	if payload.has("images") || (isCreate && len(payload.Images) > 0) {
		if err := h.Q.AdminReplaceProductImages(ctx, dbgen.AdminReplaceProductImagesParams{ProductID: id, TenantID: tenantID}); err != nil {
			return err
		}
		for i, url := range payload.Images {
			trimmed := strings.TrimSpace(url)
			if trimmed == "" {
				continue
			}
			if err := h.Q.AdminInsertProductImage(ctx, dbgen.AdminInsertProductImageParams{
				ProductID: id,
				Url:       trimmed,
				SortOrder: int32(i),
				TenantID:  tenantID,
			}); err != nil {
				return err
			}
		}
	}
	if payload.has("specs") || (isCreate && len(payload.Specs) > 0) {
		if err := h.Q.AdminDeleteProductSpecs(ctx, dbgen.AdminDeleteProductSpecsParams{ProductID: id, TenantID: tenantID}); err != nil {
			return err
		}
		for _, spec := range payload.Specs {
			key := strings.TrimSpace(spec.Key)
			value := strings.TrimSpace(spec.Value)
			if key == "" || value == "" {
				continue
			}
			if err := h.Q.AdminInsertProductSpec(ctx, dbgen.AdminInsertProductSpecParams{
				ProductID: id,
				Key:       key,
				Value:     value,
				TenantID:  tenantID,
			}); err != nil {
				return err
			}
		}
	}
	for _, variant := range payload.Variants {
		attributes := variant.Attributes
		if len(attributes) == 0 {
			attributes = []byte(`{}`)
		}
		if variant.ID != nil && strings.TrimSpace(*variant.ID) != "" {
			variantID, err := parsePGUUID(*variant.ID)
			if err != nil {
				return err
			}
			if _, err := h.Q.AdminUpdateProductVariant(ctx, dbgen.AdminUpdateProductVariantParams{
				Sku:        optionalText(variant.SKU),
				Price:      optionalInt64(variant.Price),
				Stock:      optionalInt32(variant.Stock),
				Attributes: attributes,
				ID:         variantID,
				ProductID:  id,
				TenantID:   tenantID,
			}); err != nil {
				return err
			}
			continue
		}
		price := int64(0)
		if variant.Price != nil {
			price = *variant.Price
		}
		stock := int32(0)
		if variant.Stock != nil {
			stock = *variant.Stock
		}
		if _, err := h.Q.AdminInsertProductVariant(ctx, dbgen.AdminInsertProductVariantParams{
			ProductID:  id,
			Sku:        optionalText(variant.SKU),
			Price:      price,
			Stock:      stock,
			Attributes: attributes,
			TenantID:   tenantID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (h *CatalogHandler) invalidate(ctx context.Context, slug string) {
	if h == nil || h.Cache == nil {
		return
	}
	if slug != "" {
		h.Cache.InvalidateProduct(ctx, slug)
		return
	}
	h.Cache.InvalidateList(ctx)
}

func normalizeBadges(badges []string) []string {
	if badges == nil {
		return []string{}
	}
	return badges
}

func rawJSON(payload []byte) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(payload)
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

const timeLayout = "2006-01-02T15:04:05Z07:00"
