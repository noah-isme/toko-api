package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type taxonomyDTO struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	ParentID     *string `json:"parentId,omitempty"`
	ProductCount int32   `json:"productCount"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

type taxonomyPayload struct {
	Name     *string `json:"name"`
	Slug     *string `json:"slug"`
	ParentID *string `json:"parentId"`

	fieldsPresent map[string]bool
}

func decodeTaxonomyPayload(r *http.Request) (taxonomyPayload, error) {
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return taxonomyPayload{}, err
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return taxonomyPayload{}, err
	}
	var payload taxonomyPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		return taxonomyPayload{}, err
	}
	payload.fieldsPresent = make(map[string]bool, len(raw))
	for key := range raw {
		payload.fieldsPresent[key] = true
	}
	return payload, nil
}

func (p taxonomyPayload) has(field string) bool {
	if p.fieldsPresent == nil {
		return false
	}
	return p.fieldsPresent[field]
}

// ListCategories handles GET /api/v1/admin/categories.
func (h *CatalogHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	ctx := r.Context()
	rows, err := h.Q.AdminListCategories(ctx, tenantIDFromContext(ctx))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list categories", nil)
		return
	}
	items := make([]taxonomyDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, taxonomyDTO{
			ID:           uuidString(row.ID),
			Name:         row.Name,
			Slug:         row.Slug,
			ParentID:     nullableUUID(row.ParentID),
			ProductCount: row.ProductCount,
			CreatedAt:    timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:    timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// CreateCategory handles POST /api/v1/admin/categories.
func (h *CatalogHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	payload, err := decodeTaxonomyPayload(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	name := strings.TrimSpace(derefString(payload.Name))
	if name == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required", nil)
		return
	}
	slug := slugify(derefString(payload.Slug))
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "slug could not be derived from name", nil)
		return
	}
	parentID, err := optionalUUID(payload.ParentID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid parentId", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	row, err := h.Q.AdminCreateCategory(ctx, dbgen.AdminCreateCategoryParams{
		Name:     name,
		Slug:     slug,
		ParentID: parentID,
		TenantID: tenantID,
	})
	if err != nil {
		if isUniqueViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "slug already exists", nil)
			return
		}
		if isForeignKeyViolation(err) {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unknown parent category", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create category", nil)
		return
	}
	h.invalidate(ctx, "")
	common.JSON(w, http.StatusCreated, map[string]any{"data": taxonomyDTO{
		ID:        uuidString(row.ID),
		Name:      row.Name,
		Slug:      row.Slug,
		ParentID:  nullableUUID(row.ParentID),
		CreatedAt: timestamp(row.CreatedAt).UTC().Format(timeLayout),
		UpdatedAt: timestamp(row.UpdatedAt).UTC().Format(timeLayout),
	}})
}

// UpdateCategory handles PATCH /api/v1/admin/categories/{id}.
func (h *CatalogHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	id, err := parsePGUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid category id", nil)
		return
	}
	payload, err := decodeTaxonomyPayload(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	parentID, err := optionalUUID(payload.ParentID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid parentId", nil)
		return
	}
	slug := optionalText(payload.Slug)
	if payload.has("slug") {
		normalized := slugify(derefString(payload.Slug))
		if normalized == "" {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "slug cannot be empty", nil)
			return
		}
		slug = text(normalized)
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	row, err := h.Q.AdminUpdateCategory(ctx, dbgen.AdminUpdateCategoryParams{
		Name:      optionalText(payload.Name),
		Slug:      slug,
		SetParent: payload.has("parentId"),
		ParentID:  parentID,
		ID:        id,
		TenantID:  tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "category not found", nil)
			return
		}
		if isUniqueViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "slug already exists", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update category", nil)
		return
	}
	h.invalidate(ctx, "")
	common.JSON(w, http.StatusOK, map[string]any{"data": taxonomyDTO{
		ID:        uuidString(row.ID),
		Name:      row.Name,
		Slug:      row.Slug,
		ParentID:  nullableUUID(row.ParentID),
		CreatedAt: timestamp(row.CreatedAt).UTC().Format(timeLayout),
		UpdatedAt: timestamp(row.UpdatedAt).UTC().Format(timeLayout),
	}})
}

// DeleteCategory handles DELETE /api/v1/admin/categories/{id}.
func (h *CatalogHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	id, err := parsePGUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid category id", nil)
		return
	}
	ctx := r.Context()
	affected, err := h.Q.AdminDeleteCategory(ctx, dbgen.AdminDeleteCategoryParams{ID: id, TenantID: tenantIDFromContext(ctx)})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete category", nil)
		return
	}
	if affected == 0 {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "category not found", nil)
		return
	}
	h.invalidate(ctx, "")
	w.WriteHeader(http.StatusNoContent)
}

// ListBrands handles GET /api/v1/admin/brands.
func (h *CatalogHandler) ListBrands(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	ctx := r.Context()
	rows, err := h.Q.AdminListBrands(ctx, tenantIDFromContext(ctx))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list brands", nil)
		return
	}
	items := make([]taxonomyDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, taxonomyDTO{
			ID:           uuidString(row.ID),
			Name:         row.Name,
			Slug:         row.Slug,
			ProductCount: row.ProductCount,
			CreatedAt:    timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:    timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// CreateBrand handles POST /api/v1/admin/brands.
func (h *CatalogHandler) CreateBrand(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	payload, err := decodeTaxonomyPayload(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	name := strings.TrimSpace(derefString(payload.Name))
	if name == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required", nil)
		return
	}
	slug := slugify(derefString(payload.Slug))
	if slug == "" {
		slug = slugify(name)
	}
	if slug == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "slug could not be derived from name", nil)
		return
	}
	ctx := r.Context()
	row, err := h.Q.AdminCreateBrand(ctx, dbgen.AdminCreateBrandParams{Name: name, Slug: slug, TenantID: tenantIDFromContext(ctx)})
	if err != nil {
		if isUniqueViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "brand name or slug already exists", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create brand", nil)
		return
	}
	h.invalidate(ctx, "")
	common.JSON(w, http.StatusCreated, map[string]any{"data": taxonomyDTO{
		ID:        uuidString(row.ID),
		Name:      row.Name,
		Slug:      row.Slug,
		CreatedAt: timestamp(row.CreatedAt).UTC().Format(timeLayout),
		UpdatedAt: timestamp(row.UpdatedAt).UTC().Format(timeLayout),
	}})
}

// UpdateBrand handles PATCH /api/v1/admin/brands/{id}.
func (h *CatalogHandler) UpdateBrand(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	id, err := parsePGUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid brand id", nil)
		return
	}
	payload, err := decodeTaxonomyPayload(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	slug := optionalText(payload.Slug)
	if payload.has("slug") {
		normalized := slugify(derefString(payload.Slug))
		if normalized == "" {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "slug cannot be empty", nil)
			return
		}
		slug = text(normalized)
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	row, err := h.Q.AdminUpdateBrand(ctx, dbgen.AdminUpdateBrandParams{
		Name:     optionalText(payload.Name),
		Slug:     slug,
		ID:       id,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "brand not found", nil)
			return
		}
		if isUniqueViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "brand name or slug already exists", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update brand", nil)
		return
	}
	h.invalidate(ctx, "")
	common.JSON(w, http.StatusOK, map[string]any{"data": taxonomyDTO{
		ID:        uuidString(row.ID),
		Name:      row.Name,
		Slug:      row.Slug,
		CreatedAt: timestamp(row.CreatedAt).UTC().Format(timeLayout),
		UpdatedAt: timestamp(row.UpdatedAt).UTC().Format(timeLayout),
	}})
}

// DeleteBrand handles DELETE /api/v1/admin/brands/{id}.
func (h *CatalogHandler) DeleteBrand(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	id, err := parsePGUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid brand id", nil)
		return
	}
	ctx := r.Context()
	affected, err := h.Q.AdminDeleteBrand(ctx, dbgen.AdminDeleteBrandParams{ID: id, TenantID: tenantIDFromContext(ctx)})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete brand", nil)
		return
	}
	if affected == 0 {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "brand not found", nil)
		return
	}
	h.invalidate(ctx, "")
	w.WriteHeader(http.StatusNoContent)
}
