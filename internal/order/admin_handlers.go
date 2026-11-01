package order

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// AdminOrderStore is the tenant-scoped query surface used by AdminHandler.
// *dbgen.Queries satisfies it.
type AdminOrderStore interface {
	AdminGetOrderStatus(ctx context.Context, arg dbgen.AdminGetOrderStatusParams) (dbgen.OrderStatus, error)
	AdminUpdateOrderStatusIfAllowed(ctx context.Context, arg dbgen.AdminUpdateOrderStatusIfAllowedParams) (pgtype.UUID, error)
}

// AdminHandler provides administrative order management endpoints.
type AdminHandler struct {
	Q AdminOrderStore
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
	tenantID, ok := tenantFromRequest(r)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	// Both statements filter by tenant: an order of another tenant is
	// indistinguishable from a missing one (404), so ids never leak across tenants.
	current, err := h.Q.AdminGetOrderStatus(r.Context(), dbgen.AdminGetOrderStatusParams{ID: oID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order", nil)
		return
	}
	if !isAllowedTransition(current, target) {
		common.JSONError(w, http.StatusConflict, "INVALID_STATE", "cannot transition to equal or previous state", nil)
		return
	}
	if _, err := h.Q.AdminUpdateOrderStatusIfAllowed(r.Context(), dbgen.AdminUpdateOrderStatusIfAllowedParams{ID: oID, TenantID: tenantID, Status: target}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusConflict, "INVALID_STATE", "state transition not allowed", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update order status", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// isAllowedAdminTarget lists the statuses an admin may set with PATCH status.
// EXPIRED is system-only (checkout-expiry job or a provider-reported expiry)
// and terminal, so it is neither a target here nor a source in
// allowedOrderTransitions. PAID is reached through the payment webhook or
// POST /admin/orders/{id}/confirm-payment, which also settle stock.
func isAllowedAdminTarget(status dbgen.OrderStatus) bool {
	switch status {
	case dbgen.OrderStatusPACKED, dbgen.OrderStatusSHIPPED, dbgen.OrderStatusOUTFORDELIVERY, dbgen.OrderStatusDELIVERED, dbgen.OrderStatusCANCELLED:
		return true
	}
	return false
}

// isAllowedTransition mirrors the guard in the UpdateOrderStatusIfAllowed query.
// The SQL remains the race-safe source of truth; this pre-check exists only so a
// rejected transition returns 409 with a clear reason instead of pgx.ErrNoRows.
// Cancellation moves backwards in the fulfilment order, so a monotonic rank
// comparison cannot express it.
func isAllowedTransition(current, target dbgen.OrderStatus) bool {
	allowed, ok := allowedOrderTransitions[current]
	if !ok {
		return false
	}
	for _, candidate := range allowed {
		if candidate == target {
			return true
		}
	}
	return false
}

var allowedOrderTransitions = map[dbgen.OrderStatus][]dbgen.OrderStatus{
	dbgen.OrderStatusPENDINGPAYMENT: {dbgen.OrderStatusPAID, dbgen.OrderStatusCANCELLED},
	dbgen.OrderStatusPAID:           {dbgen.OrderStatusPACKED, dbgen.OrderStatusCANCELLED},
	dbgen.OrderStatusPACKED:         {dbgen.OrderStatusSHIPPED},
	dbgen.OrderStatusSHIPPED:        {dbgen.OrderStatusOUTFORDELIVERY},
	dbgen.OrderStatusOUTFORDELIVERY: {dbgen.OrderStatusDELIVERED},
}

// tenantFromRequest resolves the tenant set by the tenant resolver middleware.
func tenantFromRequest(r *http.Request) (pgtype.UUID, bool) {
	raw, ok := tenant.FromContext(r.Context())
	if !ok {
		return pgtype.UUID{}, false
	}
	id, err := parseUUID(raw)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return id, true
}

func parseUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
