package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if closeErr := response.Body.Close(); closeErr != nil {
		return IntentResponse{}, fmt.Errorf("close xendit create invoice response: %w", closeErr)
	}
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

var (
	_ RefundProvider       = Xendit{}
	_ RefundLookupProvider = Xendit{}
)

// xenditRefundBody is a struct (not a map) so the same RefundRequest always
// encodes to byte-identical JSON: Xendit rejects an idempotency-key reused with a
// different payload.
type xenditRefundBody struct {
	PaymentRequestID string            `json:"payment_request_id,omitempty"`
	InvoiceID        string            `json:"invoice_id,omitempty"`
	ReferenceID      string            `json:"reference_id"`
	Amount           int64             `json:"amount"`
	Currency         string            `json:"currency"`
	Reason           string            `json:"reason"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type xenditRefund struct {
	ID          string `json:"id"`
	ReferenceID string `json:"reference_id"`
	Status      string `json:"status"`
	FailureCode string `json:"failure_code"`
}

// Refund creates a refund through Xendit's refunds API. req.RefundKey is sent as
// both the idempotency-key header and reference_id. The payment is addressed by
// payment_request_id when req.PaymentRef is a payment request ("pr-..."), otherwise
// by invoice_id (this integration stores the invoice id as the payment token).
func (x Xendit) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	if err := validateRefundRequest(req); err != nil {
		return RefundResult{}, err
	}
	if x.Stub {
		return RefundResult{ProviderRef: "stub-refund-" + req.RefundKey, Status: RefundStatusSucceeded}, nil
	}
	if strings.TrimSpace(x.SecretKey) == "" {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "xendit secret key is required"}
	}
	if strings.TrimSpace(req.PaymentRef) == "" {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "xendit payment reference is required"}
	}
	payload := xenditRefundBody{
		ReferenceID: req.RefundKey,
		Amount:      req.Amount,
		Currency:    xenditCurrency(req.Currency),
		Reason:      "REQUESTED_BY_CUSTOMER",
	}
	if strings.HasPrefix(req.PaymentRef, "pr-") {
		payload.PaymentRequestID = req.PaymentRef
	} else {
		payload.InvoiceID = req.PaymentRef
	}
	if note := truncateRunes(strings.TrimSpace(req.Reason), 500); note != "" {
		payload.Metadata = map[string]string{"note": note}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: err.Error()}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, x.apiHost()+"/refunds", bytes.NewReader(body))
	if err != nil {
		return RefundResult{}, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: err.Error()}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("idempotency-key", req.RefundKey)
	x.setAuth(request)
	status, responseBody, err := doLimited(x.client(), request)
	if err != nil {
		return RefundResult{}, fmt.Errorf("xendit refund: %w", err)
	}
	if status < 200 || status >= 300 {
		return RefundResult{}, xenditRefundHTTPError(status, responseBody)
	}
	var result xenditRefund
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return RefundResult{}, &RefundError{Code: "UNPARSABLE", Message: strings.TrimSpace(string(responseBody))}
	}
	if result.ID == "" {
		return RefundResult{}, &RefundError{Code: "UNPARSABLE", Message: "xendit refund response missing id"}
	}
	return xenditRefundOutcome(result)
}

// LookupRefund lists the refunds of the payment and reports the one whose
// reference_id is req.RefundKey.
func (x Xendit) LookupRefund(ctx context.Context, req RefundRequest) (RefundResult, bool, error) {
	if x.Stub {
		return RefundResult{}, false, nil
	}
	if strings.TrimSpace(x.SecretKey) == "" || strings.TrimSpace(req.PaymentRef) == "" {
		return RefundResult{}, false, &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "xendit secret key and payment reference are required"}
	}
	query := url.Values{"limit": {"100"}}
	if strings.HasPrefix(req.PaymentRef, "pr-") {
		query.Set("payment_request_id", req.PaymentRef)
	} else {
		query.Set("invoice_id", req.PaymentRef)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, x.apiHost()+"/refunds?"+query.Encode(), nil)
	if err != nil {
		return RefundResult{}, false, err
	}
	x.setAuth(request)
	status, responseBody, err := doLimited(x.client(), request)
	if err != nil {
		return RefundResult{}, false, fmt.Errorf("xendit refund lookup: %w", err)
	}
	if status == http.StatusNotFound {
		return RefundResult{}, false, nil
	}
	if status < 200 || status >= 300 {
		// A failed lookup never proves absence: keep the outcome unknown.
		return RefundResult{}, false, &RefundError{Code: "HTTP_" + strconv.Itoa(status), Message: strings.TrimSpace(string(responseBody))}
	}
	var result struct {
		Data    []xenditRefund `json:"data"`
		HasMore bool           `json:"has_more"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return RefundResult{}, false, &RefundError{Code: "UNPARSABLE", Message: strings.TrimSpace(string(responseBody))}
	}
	for _, refund := range result.Data {
		if refund.ReferenceID == req.RefundKey && refund.ID != "" {
			res, err := xenditRefundOutcome(refund)
			if err != nil {
				return RefundResult{}, true, err
			}
			return res, true, nil
		}
	}
	if result.HasMore {
		return RefundResult{}, false, &RefundError{Code: "LOOKUP_INCOMPLETE", Message: "xendit refund list has more pages"}
	}
	return RefundResult{}, false, nil
}

func xenditRefundOutcome(refund xenditRefund) (RefundResult, error) {
	switch strings.ToUpper(refund.Status) {
	case "SUCCEEDED":
		return RefundResult{ProviderRef: refund.ID, Status: RefundStatusSucceeded}, nil
	case "FAILED", "CANCELLED":
		code := refund.FailureCode
		if code == "" {
			code = strings.ToUpper(refund.Status)
		}
		return RefundResult{}, &RefundError{Definite: true, Code: code, Message: "xendit refund " + strings.ToLower(refund.Status)}
	default:
		return RefundResult{ProviderRef: refund.ID, Status: RefundStatusPending}, nil
	}
}

// xenditRefundHTTPError classifies a non-2xx answer. 5xx, 409 (duplicate or
// in-progress request under the same key), 429 and duplicate/idempotency error
// codes are indeterminate: the key may already be held by Xendit and must not be
// rotated. Other 4xx answers are definite rejections.
func xenditRefundHTTPError(status int, body []byte) error {
	var parsed struct {
		ErrorCode string `json:"error_code"`
		Message   string `json:"message"`
	}
	text := strings.TrimSpace(string(body))
	code := "HTTP_" + strconv.Itoa(status)
	message := text
	if json.Unmarshal(body, &parsed) == nil && parsed.ErrorCode != "" {
		code = parsed.ErrorCode
		message = parsed.Message
	}
	upper := strings.ToUpper(code)
	switch {
	case status >= 500, status == http.StatusConflict, status == http.StatusTooManyRequests,
		strings.Contains(upper, "DUPLICATE"), strings.Contains(upper, "IDEMPOTENCY"), strings.Contains(upper, "IN_PROGRESS"):
		return &RefundError{Code: code, Message: message}
	default:
		return &RefundError{Definite: true, Code: code, Message: message}
	}
}

func xenditCurrency(currency string) string {
	if c := strings.ToUpper(strings.TrimSpace(currency)); c != "" {
		return c
	}
	return "IDR"
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func (x Xendit) setAuth(request *http.Request) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(x.SecretKey)+":")))
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
