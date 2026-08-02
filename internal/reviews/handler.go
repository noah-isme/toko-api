package reviews

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type Handler struct {
	Svc *Service
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")

	var req struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	productID, err := h.resolveProductRef(r, productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", err.Error())
		return
	}

	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "UNAUTHORIZED", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_TENANT_ID", "invalid tenant id", err.Error())
		return
	}

	review, err := h.Svc.Create(ctx, userID, productID, tenantID, int32(req.Rating), req.Comment)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "CREATE_FAILED", "failed to create review", err.Error())
		return
	}

	common.JSON(w, http.StatusCreated, review)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	productID, err := h.resolveProductRef(r, productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_TENANT_ID", "invalid tenant id", err.Error())
		return
	}

	reviews, err := h.Svc.List(ctx, productID, tenantID, int32(page), int32(limit))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "LIST_FAILED", "failed to list reviews", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, reviews)
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")

	productID, err := h.resolveProductRef(r, productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_TENANT_ID", "invalid tenant id", err.Error())
		return
	}

	stats, err := h.Svc.Stats(ctx, productID, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "STATS_FAILED", "failed to get stats", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, stats)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "review service not configured", nil)
		return
	}
	productID, err := h.resolveProductRef(r, chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_PRODUCT_ID", "invalid product id", err.Error())
		return
	}
	userIDStr, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	tenantIDStr, ok := tenant.FromContext(r.Context())
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_TENANT_ID", "invalid tenant id", nil)
		return
	}
	reviewID, err := h.Svc.CheckUserReview(r.Context(), userID, productID, tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "review not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "DELETE_FAILED", "failed to find review", err.Error())
		return
	}
	if err := h.Svc.Delete(r.Context(), reviewID, userID, tenantID); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "DELETE_FAILED", "failed to delete review", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

// resolveProductRef accepts either a product UUID or a slug. Sibling routes
// under /products/{slug} are slug-addressed, so the reviews endpoints accept
// both rather than 400-ing on a perfectly valid product reference.
func (h *Handler) resolveProductRef(r *http.Request, value string) (pgtype.UUID, error) {
	if id, err := toUUID(value); err == nil {
		return id, nil
	}
	tenantID, ok := tenant.FromContext(r.Context())
	if !ok {
		return pgtype.UUID{}, errors.New("tenant is required")
	}
	tenantUUID, err := toUUID(tenantID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	product, err := h.Svc.Q.GetProductBySlug(r.Context(), dbgen.GetProductBySlugParams{Slug: value, TenantID: tenantUUID})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return product.ID, nil
}
