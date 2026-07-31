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
)

// CatalogHandler serves admin product, category, and brand management.
type CatalogHandler struct {
	Q     dbgen.Querier
	Cache *catalog.Cache
}

type productDTO struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
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
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
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
	search := queryText(r, "search")
	category := queryText(r, "category")
	brand := queryText(r, "brand")
	inStock := queryBool(r, "inStock")

	ctx := r.Context()
	total, err := h.Q.AdminCountProducts(ctx, dbgen.AdminCountProductsParams{
		Search:       search,
		CategorySlug: category,
		BrandSlug:    brand,
		InStock:      inStock,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count products", nil)
		return
	}
	rows, err := h.Q.AdminListProducts(ctx, dbgen.AdminListProductsParams{
		Search:       search,
		CategorySlug: category,
		BrandSlug:    brand,
		InStock:      inStock,
		LimitValue:   int32(limit),
		OffsetValue:  int32(offset),
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
			CreatedAt:    timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:    timestamp(row.UpdatedAt).UTC().Format(timeLayout),
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
	row, err := h.Q.AdminGetProduct(ctx, id)
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

	detail := productDetailDTO{
		productDTO: productDTO{
			ID:           uuidString(row.ID),
			Title:        row.Title,
			Slug:         row.Slug,
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
			CreatedAt:    timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:    timestamp(row.UpdatedAt).UTC().Format(timeLayout),
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

	ctx := r.Context()
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
	id, err := h.resolveProductID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	existing, err := h.Q.AdminGetProduct(ctx, id)
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
		ID:             id,
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
	h.invalidate(ctx, existing.Slug)
	if slug.Valid && slug.String != existing.Slug {
		h.invalidate(ctx, slug.String)
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteProduct handles DELETE /api/v1/admin/products/{id}.
func (h *CatalogHandler) DeleteProduct(w http.ResponseWriter, r *http.Request) {
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
	existing, err := h.Q.AdminGetProduct(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load product", nil)
		return
	}
	affected, err := h.Q.AdminDeleteProduct(ctx, id)
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
	id, err := h.resolveProductID(ctx, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product not found", nil)
		return
	}
	product, err := h.Q.AdminGetProduct(ctx, id)
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
	if payload.Stock != nil {
		if *payload.Stock < 0 {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "stock cannot be negative", nil)
			return
		}
		variant, err := h.Q.AdminGetPrimaryVariant(ctx, id)
		switch {
		case err == nil:
			if _, err := h.Q.AdminUpdateProductVariant(ctx, dbgen.AdminUpdateProductVariantParams{
				Stock:     pgtype.Int4{Int32: *payload.Stock, Valid: true},
				ID:        variant.ID,
				ProductID: id,
			}); err != nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update stock", nil)
				return
			}
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := h.Q.AdminInsertProductVariant(ctx, dbgen.AdminInsertProductVariantParams{
				ProductID:  id,
				Price:      product.Price,
				Stock:      *payload.Stock,
				Attributes: []byte(`{}`),
			}); err != nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create variant for stock", nil)
				return
			}
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
	if err := h.Q.AdminSetProductStockFlag(ctx, dbgen.AdminSetProductStockFlagParams{InStock: inStock, ID: id}); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update stock flag", nil)
		return
	}
	h.invalidate(ctx, product.Slug)
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
	return h.Q.AdminGetProductIDBySlug(ctx, trimmed)
}

// writeChildRows replaces images/specs and upserts variants when the payload
// includes them. On create the caller passes replaceAll so absent collections
// still produce a consistent (empty) child set.
func (h *CatalogHandler) writeChildRows(ctx context.Context, id pgtype.UUID, payload productPayload, isCreate bool) error {
	if payload.has("images") || (isCreate && len(payload.Images) > 0) {
		if err := h.Q.AdminReplaceProductImages(ctx, id); err != nil {
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
			}); err != nil {
				return err
			}
		}
	}
	if payload.has("specs") || (isCreate && len(payload.Specs) > 0) {
		if err := h.Q.AdminDeleteProductSpecs(ctx, id); err != nil {
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
