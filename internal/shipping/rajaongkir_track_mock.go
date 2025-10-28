package shipping

import "context"

// RajaOngkirMock implements Provider with deterministic events for testing/demo.
type RajaOngkirMock struct{}

// Track returns a static list of events describing a shipped parcel.
func (r RajaOngkirMock) Track(ctx context.Context, req TrackReq) ([]TrackEvent, error) {
	return []TrackEvent{{
		Status:      "SHIPPED",
		Description: "Paket diterima kurir",
		Location:    "Kediri",
		OccurredAt:  0,
	}}, nil
}
