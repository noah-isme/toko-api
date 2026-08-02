package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Midtrans implements the Provider interface for Midtrans SNAP/Payment Intent style integrations.
type Midtrans struct {
	ServerKey  string
	BaseURL    string
	Sandbox    bool
	HTTPClient *http.Client
	Stub       bool
}

// CreateIntent creates a real Midtrans SNAP transaction. Stub is intentionally
// opt-in and is only used by local tests that do not have gateway credentials.
func (m Midtrans) CreateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error) {
	if strings.TrimSpace(req.OrderID) == "" {
		return IntentResponse{}, errors.New("order id is required")
	}
	if req.Amount <= 0 {
		return IntentResponse{}, errors.New("amount must be positive")
	}
	if m.Stub {
		return m.stubIntent(req), nil
	}
	if strings.TrimSpace(m.ServerKey) == "" {
		return IntentResponse{}, errors.New("midtrans server key is required")
	}
	payload := map[string]any{
		"transaction_details": map[string]any{
			"order_id":     req.OrderID,
			"gross_amount": req.Amount,
		},
	}
	customer := map[string]string{}
	if req.CustomerName != "" {
		customer["first_name"] = req.CustomerName
	}
	if req.CustomerEmail != "" {
		customer["email"] = req.CustomerEmail
	}
	if req.CustomerPhone != "" {
		customer["phone"] = req.CustomerPhone
	}
	if len(customer) > 0 {
		payload["customer_details"] = customer
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return IntentResponse{}, err
	}
	endpoint := strings.TrimRight(m.snapHost(), "/") + "/snap/v1/transactions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return IntentResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(m.ServerKey)+":")))
	response, err := m.client().Do(request)
	if err != nil {
		return IntentResponse{}, fmt.Errorf("midtrans create transaction: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return IntentResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return IntentResponse{}, fmt.Errorf("midtrans create transaction: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var result struct {
		Token       string `json:"token"`
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return IntentResponse{}, fmt.Errorf("decode midtrans response: %w", err)
	}
	if result.Token == "" || result.RedirectURL == "" {
		return IntentResponse{}, errors.New("midtrans response missing token or redirect_url")
	}
	expiresAt := time.Now().Add(time.Duration(req.ExpiresAtSec) * time.Second)
	return IntentResponse{Provider: "midtrans", Token: result.Token, RedirectURL: result.RedirectURL, ExpiresAt: expiresAt.Unix()}, nil
}

func (m Midtrans) stubIntent(req IntentRequest) IntentResponse {
	token := fmt.Sprintf("SNAP-%s", req.OrderID)
	expiresAt := time.Now().Add(time.Duration(req.ExpiresAtSec) * time.Second)
	return IntentResponse{
		Provider:    "midtrans",
		Token:       token,
		RedirectURL: fmt.Sprintf("%s/snap/v2/vtweb/%s", strings.TrimRight(m.snapHost(), "/"), token),
		ExpiresAt:   expiresAt.Unix(),
	}
}

func (m Midtrans) client() *http.Client {
	if m.HTTPClient != nil {
		return m.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Refund calls Midtrans' server-side refund endpoint.
func (m Midtrans) Refund(ctx context.Context, orderID string, amount int64, reason string) (string, error) {
	if m.Stub {
		return "stub-refund-" + orderID, nil
	}
	if strings.TrimSpace(m.ServerKey) == "" {
		return "", errors.New("midtrans server key is required")
	}
	payload := map[string]any{"refund_key": orderID + "-refund", "reason": reason}
	if amount > 0 {
		payload["amount"] = amount
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimRight(m.apiHost(), "/") + "/v2/" + orderID + "/refund"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("transaction-source", "SNAP_API")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(m.ServerKey)+":")))
	response, err := m.client().Do(request)
	if err != nil {
		return "", fmt.Errorf("midtrans refund: %w", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("midtrans refund: status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var result struct {
		RefundKey string `json:"refund_key"`
		Status    string `json:"status_code"`
	}
	_ = json.Unmarshal(responseBody, &result)
	if result.RefundKey == "" {
		result.RefundKey = orderID + "-refund"
	}
	return result.RefundKey, nil
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

func (m Midtrans) apiHost() string {
	host := strings.TrimSpace(m.BaseURL)
	if host == "" || strings.Contains(host, "app.") {
		if m.Sandbox {
			return "https://api.sandbox.midtrans.com"
		}
		return "https://api.midtrans.com"
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
