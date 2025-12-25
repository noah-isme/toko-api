package reviews

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
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
		common.JSONError(w, http.StatusBadRequest, "invalid_body", "invalid request body", err.Error())
		return
	}

	productID, err := toUUID(productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "invalid_product_id", "invalid product id", err.Error())
		return
	}
	
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "unauthorized", "unauthorized", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "invalid_user_id", "invalid user id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "missing_tenant", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "invalid_tenant_id", "invalid tenant id", err.Error())
		return
	}

	review, err := h.Svc.Create(ctx, userID, productID, tenantID, int32(req.Rating), req.Comment)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "create_failed", "failed to create review", err.Error())
		return
	}

	common.JSON(w, http.StatusCreated, review)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

	productID, err := toUUID(productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "invalid_product_id", "invalid product id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "missing_tenant", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "invalid_tenant_id", "invalid tenant id", err.Error())
		return
	}

	reviews, err := h.Svc.List(ctx, productID, tenantID, int32(page), int32(limit))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "list_failed", "failed to list reviews", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, reviews)
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")

	productID, err := toUUID(productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "invalid_product_id", "invalid product id", err.Error())
		return
	}

	tenantIDStr, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "missing_tenant", "missing tenant context", nil)
		return
	}
	tenantID, err := toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "invalid_tenant_id", "invalid tenant id", err.Error())
		return
	}

	stats, err := h.Svc.Stats(ctx, productID, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "stats_failed", "failed to get stats", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, stats)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
