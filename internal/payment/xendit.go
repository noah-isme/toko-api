package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Xendit implements the Provider interface for a simplified invoice/checkout integration.
type Xendit struct {
	SecretKey string
	BaseURL   string
}

// CreateIntent builds a deterministic invoice identifier for testing purposes.
func (x Xendit) CreateIntent(_ context.Context, req IntentRequest) (IntentResponse, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return IntentResponse{}, errors.New("order id is required")
	}
	token := fmt.Sprintf("xendit-%s", req.OrderID)
	expiresAt := time.Now().Add(time.Duration(req.ExpiresAtSec) * time.Second)
	host := strings.TrimRight(strings.TrimSpace(x.BaseURL), "/")
	if host == "" {
		host = "https://checkout-stub.xendit"
	}
	return IntentResponse{
		Provider:    "xendit",
		Token:       token,
		RedirectURL: fmt.Sprintf("%s/%s", host, token),
		ExpiresAt:   expiresAt.Unix(),
	}, nil
}

// VerifyWebhook validates the callback signature and normalises the payload.
func (x Xendit) VerifyWebhook(r *http.Request, body []byte) (WebhookVerifyResult, error) {
	expected := x.computeSignature(body)
	provided := strings.TrimSpace(r.Header.Get("x-callback-signature"))
	if expected == "" || provided == "" || !hmac.Equal([]byte(expected), []byte(provided)) {
		return WebhookVerifyResult{Valid: false, Err: errors.New("invalid signature")}, nil
	}

	var payload struct {
		ExternalID string      `json:"external_id"`
		Amount     json.Number `json:"amount"`
		Status     string      `json:"status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebhookVerifyResult{Valid: false, Err: err}, nil
	}

	orderID := payload.ExternalID
	if orderID == "" {
		orderID = payload.Status // fallback to avoid empty value but maintain debug ability
	}

	amount, _ := payload.Amount.Int64()
	if amount == 0 {
		if f, err := payload.Amount.Float64(); err == nil {
			amount = int64(f)
		}
	}

	status := normaliseXenditStatus(payload.Status)

	return WebhookVerifyResult{
		Valid:           true,
		OrderID:         orderID,
		Amount:          amount,
		Status:          status,
		ProviderPayload: body,
	}, nil
}

func (x Xendit) computeSignature(body []byte) string {
	key := strings.TrimSpace(x.SecretKey)
	if key == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func normaliseXenditStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "paid", "settled", "success":
		return "PAID"
	case "pending", "invoice.paid_pending_verification":
		return "PENDING"
	case "expired":
		return "EXPIRED"
	case "failed", "canceled":
		return "FAILED"
	default:
		return "PENDING"
	}
}
