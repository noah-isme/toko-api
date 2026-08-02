package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Xendit implements the Provider interface for a simplified invoice/checkout integration.
type Xendit struct {
	SecretKey     string
	BaseURL       string
	CallbackToken string
	HTTPClient    *http.Client
	Stub          bool
}

// CreateIntent creates a real Xendit invoice.
func (x Xendit) CreateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return IntentResponse{}, errors.New("order id is required")
	}
	if req.Amount <= 0 {
		return IntentResponse{}, errors.New("amount must be positive")
	}
	if x.Stub {
		return x.stubIntent(req), nil
	}
	if strings.TrimSpace(x.SecretKey) == "" {
		return IntentResponse{}, errors.New("xendit secret key is required")
	}
	payload := map[string]any{
		"external_id": req.OrderID,
		"amount":      req.Amount,
		"description": "Toko order " + req.OrderID,
	}
	if req.CustomerEmail != "" {
		payload["payer_email"] = req.CustomerEmail
	}
	if req.ExpiresAtSec > 0 {
		payload["invoice_duration"] = req.ExpiresAtSec
	}
	if req.CallbackBaseURL != "" {
		payload["success_redirect_url"] = strings.TrimRight(req.CallbackBaseURL, "/") + "/payment/success"
		payload["failure_redirect_url"] = strings.TrimRight(req.CallbackBaseURL, "/") + "/payment/failed"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return IntentResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(x.apiHost(), "/")+"/v2/invoices", bytes.NewReader(body))
	if err != nil {
		return IntentResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(x.SecretKey)+":")))
	response, err := x.client().Do(request)
	if err != nil {
		return IntentResponse{}, fmt.Errorf("xendit create invoice: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return IntentResponse{}, fmt.Errorf("xendit create invoice: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var result struct {
		ID         string    `json:"id"`
		InvoiceURL string    `json:"invoice_url"`
		ExpiryDate time.Time `json:"expiry_date"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return IntentResponse{}, fmt.Errorf("decode xendit response: %w", err)
	}
	if result.ID == "" || result.InvoiceURL == "" {
		return IntentResponse{}, errors.New("xendit response missing id or invoice_url")
	}
	expiresAt := result.ExpiryDate.Unix()
	if result.ExpiryDate.IsZero() {
		expiresAt = time.Now().Add(time.Duration(req.ExpiresAtSec) * time.Second).Unix()
	}
	return IntentResponse{Provider: "xendit", Token: result.ID, RedirectURL: result.InvoiceURL, ExpiresAt: expiresAt}, nil
}

func (x Xendit) stubIntent(req IntentRequest) IntentResponse {
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
	}
}

func (x Xendit) client() *http.Client {
	if x.HTTPClient != nil {
		return x.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}
func (x Xendit) apiHost() string {
	host := strings.TrimRight(strings.TrimSpace(x.BaseURL), "/")
	if host == "" {
		return "https://api.xendit.co"
	}
	return host
}

// VerifyWebhook validates the callback signature and normalises the payload.
func (x Xendit) VerifyWebhook(r *http.Request, body []byte) (WebhookVerifyResult, error) {
	provided := strings.TrimSpace(r.Header.Get("x-callback-token"))
	expected := strings.TrimSpace(x.CallbackToken)
	if expected == "" {
		expected = strings.TrimSpace(x.SecretKey)
	}
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
