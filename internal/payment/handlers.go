package payment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Handler exposes HTTP endpoints for payment intents and status polling.
type Handler struct {
	Svc *Service
	Q   *dbgen.Queries
}

type intentReq struct {
	OrderID string `json:"orderId"`
	Channel string `json:"channel"`
}

type intentResp struct {
	Provider    string     `json:"provider"`
	Token       string     `json:"token,omitempty"`
	RedirectURL string     `json:"redirectUrl,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

// Intent creates (or reuses) a payment intent for the authenticated user's order.
func (h *Handler) Intent(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Svc == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "payment handler unavailable", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || strings.TrimSpace(userID) == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "login required", nil)
		return
	}
	var req intentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid body", nil)
		return
	}
	req.OrderID = strings.TrimSpace(req.OrderID)
	if req.OrderID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "orderId is required", nil)
		return
	}
	orderUUID, err := cart.ToUUID(req.OrderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orderId", nil)
		return
	}
	userUUID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user", nil)
		return
	}
	order, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: orderUUID, UserID: userUUID})
	if err != nil {
		common.JSONError(w, http.StatusNotFound, "ORDER_NOT_FOUND", "order not found", nil)
		return
	}
	payment, err := h.Svc.CreateIntent(r.Context(), req.OrderID, order.PricingTotal, req.Channel, h.Svc.CallbackBaseURL)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			status = http.StatusGatewayTimeout
		}
		common.JSONError(w, status, "INTENT_FAILED", err.Error(), nil)
		return
	}
	resp := intentResp{
		Provider:    payment.Provider.String,
		Token:       payment.IntentToken.String,
		RedirectURL: payment.RedirectUrl.String,
	}
	if payment.ExpiresAt.Valid {
		t := payment.ExpiresAt.Time
		resp.ExpiresAt = &t
	}
	common.JSON(w, http.StatusOK, resp)
}

// Status reports the consolidated payment status for an order belonging to the authenticated user.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Svc == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "payment handler unavailable", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || strings.TrimSpace(userID) == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "login required", nil)
		return
	}
	orderID := strings.TrimSpace(chi.URLParam(r, "orderId"))
	if orderID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "orderId is required", nil)
		return
	}
	orderUUID, err := cart.ToUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orderId", nil)
		return
	}
	userUUID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user", nil)
		return
	}
	if _, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: orderUUID, UserID: userUUID}); err != nil {
		common.JSONError(w, http.StatusNotFound, "ORDER_NOT_FOUND", "order not found", nil)
		return
	}
	status, err := h.Svc.ConsolidatedStatus(r.Context(), orderID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "STATUS_ERROR", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]string{"status": status})
}
