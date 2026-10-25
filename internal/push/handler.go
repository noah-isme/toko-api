package push

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/common"
)

type Handler struct {
	Svc *Service
}

// Limits that keep a stored subscription row bounded.
const (
	maxEndpointBytes = 2048
	maxKeyBytes      = 512
)

func writePushDisabled(w http.ResponseWriter) {
	common.JSONError(w, http.StatusServiceUnavailable, "PUSH_DISABLED", "web push is not configured on this server", nil)
}

// GetVapidPublicKey returns the VAPID application server key. Both field
// spellings are returned: the OpenAPI contract names it public_key, the
// storefront reads publicKey.
func (h *Handler) GetVapidPublicKey(w http.ResponseWriter, r *http.Request) {
	if !h.Svc.Enabled() {
		writePushDisabled(w)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"public_key": h.Svc.PublicKey,
		"publicKey":  h.Svc.PublicKey,
	})
}

func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		common.JSONError(w, http.StatusBadRequest, "MISSING_FIELDS", "endpoint and keys are required", nil)
		return
	}
	if len(req.Endpoint) > maxEndpointBytes || len(req.Keys.P256dh) > maxKeyBytes || len(req.Keys.Auth) > maxKeyBytes {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "endpoint or keys too long", nil)
		return
	}

	_, err = h.Svc.CreateSubscription(ctx, userID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth)
	if err != nil {
		if errors.Is(err, ErrInvalidEndpoint) {
			// The details carry the validation reason only, never the endpoint.
			common.JSONError(w, http.StatusBadRequest, "INVALID_ENDPOINT", "endpoint must be an https URL of a known push service", err.Error())
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "SUBSCRIBE_FAILED", "failed to subscribe", nil)
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Subscribed successfully",
	})
}

func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	endpoint := r.URL.Query().Get("endpoint")
	if endpoint == "" {
		// Delete all subscriptions for user
		err = h.Svc.DeleteAllSubscriptions(ctx, userID)
	} else {
		err = h.Svc.DeleteSubscription(ctx, userID, endpoint)
	}

	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "UNSUBSCRIBE_FAILED", "failed to unsubscribe", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Unsubscribed successfully",
	})
}

func (h *Handler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	prefs, err := h.Svc.GetPreferences(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Return defaults
			common.JSON(w, http.StatusOK, map[string]any{
				"enabled": true,
				"types": map[string]bool{
					"order_update":  true,
					"promo_updates": true,
					"stock_updates": true,
				},
			})
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "GET_FAILED", "failed to get preferences", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"enabled": prefs.Enabled,
		"types": map[string]bool{
			"order_update":  prefs.OrderUpdates,
			"promo_updates": prefs.PromoUpdates,
			"stock_updates": prefs.StockUpdates,
		},
	})
}

func (h *Handler) UpdatePreferences(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}

	var req struct {
		Enabled *bool `json:"enabled"`
		Types   struct {
			OrderUpdate  *bool `json:"order_update"`
			PromoUpdates *bool `json:"promo_updates"`
			StockUpdates *bool `json:"stock_updates"`
		} `json:"types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	enabled := true
	orderUpdates := true
	promoUpdates := true
	stockUpdates := true

	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.Types.OrderUpdate != nil {
		orderUpdates = *req.Types.OrderUpdate
	}
	if req.Types.PromoUpdates != nil {
		promoUpdates = *req.Types.PromoUpdates
	}
	if req.Types.StockUpdates != nil {
		stockUpdates = *req.Types.StockUpdates
	}

	prefs, err := h.Svc.UpsertPreferences(ctx, userID, enabled, orderUpdates, promoUpdates, stockUpdates)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "UPDATE_FAILED", "failed to update preferences", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Preferences updated",
		"enabled": prefs.Enabled,
		"types": map[string]bool{
			"order_update":  prefs.OrderUpdates,
			"promo_updates": prefs.PromoUpdates,
			"stock_updates": prefs.StockUpdates,
		},
	})
}

// SendTest synchronously sends a test push to every subscription of the
// caller, ignoring per-type preferences (explicit debug action).
func (h *Handler) SendTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userIDStr, ok := common.UserID(ctx)
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "login required", nil)
		return
	}
	userID, err := toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INVALID_USER_ID", "invalid user id", err.Error())
		return
	}
	if !h.Svc.Enabled() {
		writePushDisabled(w)
		return
	}
	result, err := h.Svc.SendToUser(ctx, userID, Message{
		Title: "Notifikasi uji",
		Body:  "Push notification berfungsi.",
		Tag:   "test",
		Data:  map[string]any{"url": "/account/notifications"},
	})
	switch {
	case errors.Is(err, ErrPushDisabled):
		writePushDisabled(w)
		return
	case errors.Is(err, ErrNoSubscriptions):
		common.JSONError(w, http.StatusNotFound, "NO_SUBSCRIPTION", "no push subscription found; subscribe first", nil)
		return
	case err != nil && result.Sent+result.Failed+result.Pruned == 0:
		// Loading subscriptions failed before anything was sent.
		common.JSONError(w, http.StatusInternalServerError, "PUSH_SEND_FAILED", "failed to send test notification", nil)
		return
	}
	if result.Sent == 0 {
		common.JSONError(w, http.StatusBadGateway, "PUSH_SEND_FAILED", "no push service accepted the test notification", result)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Test notification sent",
		"sent":    result.Sent,
		"failed":  result.Failed,
		"pruned":  result.Pruned,
	})
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
