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
	"net/url"
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
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if err != nil {
		return IntentResponse{}, err
	}
	if closeErr != nil {
		return IntentResponse{}, fmt.Errorf("close midtrans response: %w", closeErr)
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

var (
	_ RefundProvider       = Midtrans{}
	_ RefundLookupProvider = Midtrans{}
)

// Refund calls Midtrans' server-side refund endpoint with req.RefundKey as the
// refund_key. Midtrans answers HTTP 200 for most outcomes, so the body status_code
// decides: "200" is an accepted refund; "202" (denied), "412", "407" and other 4xx
// codes are definite rejections; "201", any 5xx (HTTP status or body status_code),
// transport errors and unparsable bodies are
// indeterminate and must be looked up (LookupRefund) before the key is sent again.
func (m Midtrans) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	if err := validateRefundRequest(req); err != nil {
		return RefundResult{}, err
	}
	if strings.TrimSpace(req.OrderID) == "" {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "order id is required"}
	}
	if m.Stub {
		return RefundResult{ProviderRef: "stub-refund-" + req.RefundKey, Status: RefundStatusSucceeded}, nil
	}
	if strings.TrimSpace(m.ServerKey) == "" {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "midtrans server key is required"}
	}
	payload := map[string]any{"refund_key": req.RefundKey, "amount": req.Amount}
	if req.Reason != "" {
		payload["reason"] = req.Reason
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: err.Error()}
	}
	endpoint := strings.TrimRight(m.apiHost(), "/") + "/v2/" + url.PathEscape(req.OrderID) + "/refund"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	m.setAPIHeaders(request)
	status, responseBody, err := doLimited(m.client(), request)
	if err != nil {
		return RefundResult{}, fmt.Errorf("midtrans refund: %w", err)
	}
	text := strings.TrimSpace(string(responseBody))
	if status >= 500 {
		return RefundResult{}, &RefundError{Code: strconv.Itoa(status), Message: truncateProviderMessage(text)}
	}
	var result struct {
		RefundKey         string `json:"refund_key"`
		StatusCode        string `json:"status_code"`
		StatusMessage     string `json:"status_message"`
		TransactionStatus string `json:"transaction_status"`
	}
	decodeErr := json.Unmarshal(responseBody, &result)
	if status < 200 || status >= 300 {
		if decodeErr != nil || result.StatusCode == "" {
			return RefundResult{}, midtransRefundRejection(strconv.Itoa(status), text)
		}
		return RefundResult{}, midtransRefundRejection(result.StatusCode, result.StatusMessage)
	}
	if decodeErr != nil {
		return RefundResult{}, &RefundError{Code: "UNPARSABLE", Message: truncateProviderMessage(text)}
	}
	switch result.StatusCode {
	case "200":
		switch strings.ToLower(result.TransactionStatus) {
		case "", "refund", "partial_refund":
		default:
			// A 200 that does not report a refund state is not proof the refund applied.
			return RefundResult{}, &RefundError{Code: "UNEXPECTED_STATUS", Message: "midtrans refund answered 200 with transaction_status " + result.TransactionStatus}
		}
		ref := result.RefundKey
		if ref == "" {
			ref = req.RefundKey
		}
		return RefundResult{ProviderRef: ref, Status: RefundStatusSucceeded}, nil
	case "201":
		// Not verified against a sandbox refund response: treat as unknown so the
		// next attempt looks the key up instead of assuming money moved.
		return RefundResult{}, &RefundError{Code: "201", Message: result.StatusMessage}
	default:
		return RefundResult{}, midtransRefundRejection(result.StatusCode, result.StatusMessage)
	}
}

// midtransRefundRejection classifies an explicit Midtrans error answer. Rejections
// that may describe a key Midtrans already holds (duplicate refund_key) are kept
// indeterminate so the key is never rotated on them.
// Any server-class code (>= 500), whether it came as the HTTP status or inside an
// HTTP 200 body, is indeterminate: the bank or Midtrans may have applied the refund.
func midtransRefundRejection(code, message string) error {
	if code == "" {
		code = "MISSING_STATUS_CODE"
	}
	message = truncateProviderMessage(message)
	if n, err := strconv.Atoi(strings.TrimSpace(code)); err == nil && n >= 500 {
		return &RefundError{Code: code, Message: message}
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "duplicate") || strings.Contains(lower, "already") {
		return &RefundError{Code: code, Message: message}
	}
	return &RefundError{Definite: true, Code: code, Message: message}
}

// LookupRefund asks Midtrans' transaction status endpoint whether req.RefundKey is
// among the refunds applied to req.OrderID.
func (m Midtrans) LookupRefund(ctx context.Context, req RefundRequest) (RefundResult, bool, error) {
	if m.Stub {
		return RefundResult{}, false, nil
	}
	if strings.TrimSpace(m.ServerKey) == "" {
		return RefundResult{}, false, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "midtrans server key is required"}
	}
	endpoint := strings.TrimRight(m.apiHost(), "/") + "/v2/" + url.PathEscape(req.OrderID) + "/status"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return RefundResult{}, false, err
	}
	m.setAPIHeaders(request)
	status, responseBody, err := doLimited(m.client(), request)
	if err != nil {
		return RefundResult{}, false, fmt.Errorf("midtrans refund lookup: %w", err)
	}
	if status < 200 || status >= 300 {
		if status == http.StatusNotFound {
			return RefundResult{}, false, nil
		}
		return RefundResult{}, false, &RefundError{Code: strconv.Itoa(status), Message: truncateProviderMessage(string(responseBody))}
	}
	var result struct {
		StatusCode string `json:"status_code"`
		Refunds    []struct {
			RefundKey string `json:"refund_key"`
		} `json:"refunds"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return RefundResult{}, false, &RefundError{Code: "UNPARSABLE", Message: truncateProviderMessage(string(responseBody))}
	}
	switch result.StatusCode {
	case "404":
		return RefundResult{}, false, nil
	case "200":
		for _, refund := range result.Refunds {
			if refund.RefundKey == req.RefundKey {
				return RefundResult{ProviderRef: refund.RefundKey, Status: RefundStatusSucceeded}, true, nil
			}
		}
		return RefundResult{}, false, nil
	default:
		return RefundResult{}, false, &RefundError{Code: result.StatusCode, Message: "midtrans status lookup did not succeed"}
	}
}

func (m Midtrans) setAPIHeaders(request *http.Request) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("transaction-source", "SNAP_API")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(m.ServerKey)+":")))
}

// doLimited sends request and reads at most 1 MiB of the response body. Every
// error (transport, read, close) is returned: callers treat them as indeterminate.
func doLimited(client *http.Client, request *http.Request) (int, []byte, error) {
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return response.StatusCode, nil, fmt.Errorf("read response: %w", readErr)
	}
	if closeErr != nil {
		return response.StatusCode, nil, fmt.Errorf("close response: %w", closeErr)
	}
	return response.StatusCode, body, nil
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
	case "partial_refund":
		return "PARTIALLY_REFUNDED"
	default:
		return "PENDING"
	}
}
