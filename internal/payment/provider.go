package payment

import (
	"context"
	"net/http"
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

// RefundProvider is implemented by gateways that support server-side refunds.
// Keeping refunds separate lets an installation expose checkout providers that
// do not support refunds without weakening the payment intent contract.
type RefundProvider interface {
	Provider
	Refund(ctx context.Context, orderID string, amount int64, reason string) (string, error)
}
