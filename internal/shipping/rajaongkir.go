package shipping

import "context"

// RateReq describes a shipping rate request.
type RateReq struct {
	Origin      string
	Destination string
	WeightGram  int
	Courier     string
}

// Rate describes a returned shipping rate option.
type Rate struct {
	Service string `json:"service"`
	Price   int64  `json:"cost"`
	ETD     string `json:"etd"`
	Courier string `json:"courier,omitempty"`
}

// Client defines the behaviour required to quote shipping rates.
type Client interface {
	Rates(ctx context.Context, r RateReq) ([]Rate, error)
}

// MockClient returns static rates and is useful for testing and development.
type MockClient struct{}

// Rates returns canned rates regardless of the request payload.
// Rates returns canned rates regardless of the request payload.
func (MockClient) Rates(ctx context.Context, r RateReq) ([]Rate, error) {
	_ = ctx
	return []Rate{
		{Service: "REG", Price: 15000, ETD: "2-3", Courier: r.Courier},
		{Service: "YES", Price: 30000, ETD: "1", Courier: r.Courier},
	}, nil
}
