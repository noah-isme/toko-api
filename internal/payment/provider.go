package payment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// IntentRequest captures the information required to open a payment intent with a provider.
type IntentRequest struct {
	OrderID         string
	Amount          int64
	Channel         string
	ExpiresAtSec    int
	CallbackBaseURL string
	CustomerName    string
	CustomerEmail   string
	CustomerPhone   string
}

// IntentResponse represents the minimal information returned by a provider when creating an intent.
type IntentResponse struct {
	Provider    string
	Token       string
	RedirectURL string
	ExpiresAt   int64
	// ExpirySynced reports that the provider was told to close the
	// transaction at ExpiresAt. It is stored in payments.provider_payload
	// (response.ExpirySynced); Midtrans intents without it were created before
	// the Snap expiry was sent and stay payable for Midtrans' default window,
	// which the checkout-expiry job honours.
	ExpirySynced bool
}

// WebhookVerifyResult contains the normalised data extracted from a webhook notification after signature verification.
type WebhookVerifyResult struct {
	Valid           bool
	OrderID         string
	Amount          int64
	Status          string
	ProviderPayload []byte
	Err             error
}

// Provider abstracts the operations required from an upstream payment provider.
type Provider interface {
	CreateIntent(ctx context.Context, req IntentRequest) (IntentResponse, error)
	VerifyWebhook(r *http.Request, body []byte) (WebhookVerifyResult, error)
}

// RefundRequest describes one refund attempt. RefundKey is the caller's stable,
// unique key for the attempt; it is sent to the gateway as its idempotency key so a
// retry of the same attempt can never move money twice. Callers must send a
// byte-identical request (same amount, reason) when they retry with the same key.
type RefundRequest struct {
	OrderID    string
	PaymentRef string // provider token stored on the payment (e.g. Xendit invoice id)
	RefundKey  string
	Amount     int64
	Currency   string
	Reason     string
}

// Refund statuses reported in RefundResult.Status.
const (
	RefundStatusSucceeded = "SUCCEEDED"
	RefundStatusPending   = "PENDING"
)

// RefundResult is a refund the gateway accepted. Status is RefundStatusSucceeded or
// RefundStatusPending (accepted, settles asynchronously).
type RefundResult struct {
	ProviderRef string
	Status      string
}

// RefundError is a gateway answer to a refund request. Definite=true means the
// gateway rejected the refund (no money moved) and the same key must not be reused;
// Definite=false means the outcome is unknown and the same key must be retried (or
// looked up) before any new key is used.
type RefundError struct {
	Definite bool
	Code     string
	Message  string
}

func (e *RefundError) Error() string {
	kind := "indeterminate"
	if e.Definite {
		kind = "rejected"
	}
	if e.Message == "" {
		return fmt.Sprintf("refund %s: %s", kind, e.Code)
	}
	return fmt.Sprintf("refund %s: %s: %s", kind, e.Code, e.Message)
}

// IsDefiniteRefundFailure reports whether err is a RefundError the gateway answered
// definitively. Any other error (transport, timeout, context) is indeterminate.
func IsDefiniteRefundFailure(err error) bool {
	var re *RefundError
	return errors.As(err, &re) && re.Definite
}

// RefundErrorCode returns the gateway code of a RefundError, or "" for other errors.
func RefundErrorCode(err error) string {
	var re *RefundError
	if errors.As(err, &re) {
		return re.Code
	}
	return ""
}

func validateRefundRequest(req RefundRequest) error {
	if strings.TrimSpace(req.RefundKey) == "" {
		return &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "refund key is required"}
	}
	if req.Amount <= 0 {
		return &RefundError{Definite: true, Code: "INVALID_REQUEST", Message: "refund amount must be positive"}
	}
	return nil
}

// RefundProvider is implemented by gateways that support server-side refunds.
// Keeping refunds separate lets an installation expose checkout providers that
// do not support refunds without weakening the payment intent contract.
type RefundProvider interface {
	Provider
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
}

// RefundLookupProvider reports whether a refund key was already applied by the
// gateway. It is consulted before a key whose outcome is unknown is sent again.
// found=false with a nil error means the gateway has no refund under that key.
// A refund found in a failed state is reported as a definite *RefundError.
type RefundLookupProvider interface {
	LookupRefund(ctx context.Context, req RefundRequest) (result RefundResult, found bool, err error)
}
