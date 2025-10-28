package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Midtrans implements the Provider interface for Midtrans SNAP/Payment Intent style integrations.
type Midtrans struct {
	ServerKey string
	BaseURL   string
	Sandbox   bool
}

// CreateIntent issues a minimal SNAP-like response without performing a network call.
// The real implementation should call Midtrans API, but for integration tests we synthesise
// a deterministic token to drive the rest of the flow.
func (m Midtrans) CreateIntent(_ context.Context, req IntentRequest) (IntentResponse, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return IntentResponse{}, errors.New("order id is required")
	}
	token := fmt.Sprintf("SNAP-%s", req.OrderID)
	expiresAt := time.Now().Add(time.Duration(req.ExpiresAtSec) * time.Second)
	return IntentResponse{
		Provider:    "midtrans",
		Token:       token,
		RedirectURL: fmt.Sprintf("%s/snap/v2/vtweb/%s", strings.TrimRight(m.snapHost(), "/"), token),
		ExpiresAt:   expiresAt.Unix(),
	}, nil
}

func (m Midtrans) snapHost() string {
	host := strings.TrimSpace(m.BaseURL)
	if host == "" {
		if m.Sandbox {
			return "https://app.sandbox.midtrans.com"
		}
		return "https://app.midtrans.com"
	}
	return host
}

// VerifyWebhook validates the Midtrans signature and normalises the payload into WebhookVerifyResult.
func (m Midtrans) VerifyWebhook(_ *http.Request, body []byte) (WebhookVerifyResult, error) {
	var payload struct {
		OrderID           string `json:"order_id"`
		StatusCode        string `json:"status_code"`
		GrossAmount       string `json:"gross_amount"`
		SignatureKey      string `json:"signature_key"`
		TransactionStatus string `json:"transaction_status"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return WebhookVerifyResult{Valid: false, Err: err}, nil
	}

	if payload.OrderID == "" {
		return WebhookVerifyResult{Valid: false, Err: errors.New("missing order id")}, nil
	}

	expected := m.computeSignature(payload.OrderID, payload.StatusCode, payload.GrossAmount)
	provided := strings.TrimSpace(payload.SignatureKey)
	if expected == "" || provided == "" || !hmac.Equal([]byte(expected), []byte(provided)) {
		return WebhookVerifyResult{Valid: false, Err: errors.New("invalid signature")}, nil
	}

	amount, err := parseMidtransAmount(payload.GrossAmount)
	if err != nil {
		return WebhookVerifyResult{Valid: false, Err: err}, nil
	}

	status := normaliseMidtransStatus(payload.TransactionStatus)

	return WebhookVerifyResult{
		Valid:           true,
		OrderID:         payload.OrderID,
		Amount:          amount,
		Status:          status,
		ProviderPayload: body,
	}, nil
}

func (m Midtrans) computeSignature(orderID, statusCode, grossAmount string) string {
	key := strings.TrimSpace(m.ServerKey)
	if key == "" {
		return ""
	}
	mac := hmac.New(sha512.New, []byte(key))
	mac.Write([]byte(orderID))
	mac.Write([]byte(statusCode))
	mac.Write([]byte(grossAmount))
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

func parseMidtransAmount(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	if !strings.Contains(trimmed, ".") {
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}
	f, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f)), nil
}

func normaliseMidtransStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "capture", "settlement":
		return "PAID"
	case "pending":
		return "PENDING"
	case "deny", "cancel":
		return "FAILED"
	case "expire":
		return "EXPIRED"
	case "refund":
		return "REFUNDED"
	default:
		return "PENDING"
	}
}
