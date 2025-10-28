package order

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// AdminHandler provides administrative order management endpoints.
type AdminHandler struct {
	Q *dbgen.Queries
}

type patchStatusRequest struct {
	Status string `json:"status"`
}

// PatchStatus updates the order status with state-machine validation.
func (h *AdminHandler) PatchStatus(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "order queries not configured", nil)
		return
	}
	orderID := chi.URLParam(r, "id")
	oID, err := parseUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	var req patchStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if req.Status == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "status is required", nil)
		return
	}
	target := dbgen.OrderStatus(req.Status)
	if !isAllowedAdminTarget(target) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unsupported status", nil)
		return
	}
	current, err := h.Q.GetOrderStatus(r.Context(), oID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order", nil)
		return
	}
	if orderStatusRank(current) >= orderStatusRank(target) {
		common.JSONError(w, http.StatusConflict, "INVALID_STATE", "cannot transition to equal or previous state", nil)
		return
	}
	if _, err := h.Q.UpdateOrderStatusIfAllowed(r.Context(), dbgen.UpdateOrderStatusIfAllowedParams{ID: oID, Status: target}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusConflict, "INVALID_STATE", "state transition not allowed", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update order status", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isAllowedAdminTarget(status dbgen.OrderStatus) bool {
	switch status {
	case dbgen.OrderStatusPACKED, dbgen.OrderStatusSHIPPED, dbgen.OrderStatusOUTFORDELIVERY, dbgen.OrderStatusDELIVERED, dbgen.OrderStatusCANCELED:
		return true
	}
	return false
}

func orderStatusRank(status dbgen.OrderStatus) int {
	switch status {
	case dbgen.OrderStatusPENDINGPAYMENT:
		return 0
	case dbgen.OrderStatusPAID:
		return 1
	case dbgen.OrderStatusPACKED:
		return 2
	case dbgen.OrderStatusSHIPPED:
		return 3
	case dbgen.OrderStatusOUTFORDELIVERY:
		return 4
	case dbgen.OrderStatusDELIVERED:
		return 5
	case dbgen.OrderStatusCANCELED:
		return -1
	default:
		return -2
	}
}

func parseUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
