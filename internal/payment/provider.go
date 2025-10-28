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
}

// IntentResponse represents the minimal information returned by a provider when creating an intent.
type IntentResponse struct {
	Provider    string
	Token       string
	RedirectURL string
	ExpiresAt   int64
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
