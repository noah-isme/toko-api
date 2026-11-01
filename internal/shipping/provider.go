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

// TrackResult is a provider's tracking answer with an optional terminal
// state ("RETURNED" or "LOST") that ends polling without applying events.
type TrackResult struct {
	Events   []TrackEvent
	Terminal string
}

// TrackerWithSummary is implemented by providers that can report a terminal
// carrier state in addition to events.
type TrackerWithSummary interface {
	TrackWithSummary(ctx context.Context, req TrackReq) (TrackResult, error)
}
