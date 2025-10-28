package shipping

import "context"

// TrackReq encapsulates tracking lookup parameters for a shipment provider.
type TrackReq struct {
	Courier        string
	TrackingNumber string
}

// TrackEvent represents a single tracking event returned by a provider.
type TrackEvent struct {
	Status      string
	Description string
	Location    string
	OccurredAt  int64
}

// Provider models a tracking provider capable of fetching tracking events.
type Provider interface {
	Track(ctx context.Context, req TrackReq) ([]TrackEvent, error)
}
