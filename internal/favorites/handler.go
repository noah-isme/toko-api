package favorites

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type Handler struct {
	Svc *Service
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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

	favs, err := h.Svc.List(ctx, userID, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "list_failed", "failed to list favorites", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, favs)
}

func (h *Handler) Toggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		ProductID string `json:"productId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "invalid_body", "invalid request body", err.Error())
		return
	}

	productID, err := toUUID(req.ProductID)
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

	// Check if exists, then toggle
	exists, _ := h.Svc.Check(ctx, userID, productID, tenantID)
	if exists {
		if err := h.Svc.Remove(ctx, userID, productID, tenantID); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "remove_failed", "failed to remove favorite", err.Error())
			return
		}
	} else {
		if err := h.Svc.Add(ctx, userID, productID, tenantID); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "add_failed", "failed to add favorite", err.Error())
			return
		}
	}

	common.JSON(w, http.StatusOK, map[string]bool{"favorited": !exists})
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	productIDStr := chi.URLParam(r, "id")
	productID, err := toUUID(productIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "invalid_product_id", "invalid product id", err.Error())
		return
	}
	
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		// If not logged in, return false
		common.JSON(w, http.StatusOK, map[string]bool{"favorited": false})
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

	exists, err := h.Svc.Check(ctx, userID, productID, tenantID)
	if err != nil && err != pgx.ErrNoRows {
		common.JSONError(w, http.StatusInternalServerError, "check_failed", "failed to check favorite", err.Error())
		return
	}
	
	common.JSON(w, http.StatusOK, map[string]bool{"favorited": exists})
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
