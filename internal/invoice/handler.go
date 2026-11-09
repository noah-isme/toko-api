package invoice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Builder renders an invoice; *Service satisfies it.
type Builder interface {
	Build(ctx context.Context, tenantID, orderID, userID pgtype.UUID) (Result, error)
}

// Handler serves invoice PDFs.
type Handler struct {
	Svc Builder
}

// UserInvoice handles GET /orders/{orderId}/invoice for the order owner.
func (h *Handler) UserInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rawUser, ok := common.UserID(ctx)
	if !ok || rawUser == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	userID, err := toUUID(rawUser)
	if err != nil {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return
	}
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	orderID, err := toUUID(chi.URLParam(r, "orderId"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	h.serve(w, r, tenantID, orderID, userID)
}

// AdminInvoice handles GET /admin/orders/{id}/invoice. Role checks and audit
// logging come from the admin route group.
func (h *Handler) AdminInvoice(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromContext(w, r)
	if !ok {
		return
	}
	orderID, err := toUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	h.serve(w, r, tenantID, orderID, pgtype.UUID{})
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, tenantID, orderID, userID pgtype.UUID) {
	if h == nil || h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "invoice service not configured", nil)
		return
	}
	res, err := h.Svc.Build(r.Context(), tenantID, orderID, userID)
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
		return
	case errors.Is(err, ErrNotEligible):
		common.JSONError(w, http.StatusConflict, "INVOICE_NOT_AVAILABLE", "invoice is available once the order is paid", nil)
		return
	default:
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to generate invoice", nil)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "application/pdf")
	hdr.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", res.Filename))
	hdr.Set("Cache-Control", "private, no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Length", strconv.Itoa(len(res.PDF)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.PDF)
}

func tenantFromContext(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	raw, ok := tenant.FromContext(r.Context())
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return pgtype.UUID{}, false
	}
	id, err := toUUID(raw)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_TENANT_ID", "invalid tenant id", nil)
		return pgtype.UUID{}, false
	}
	return id, true
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
