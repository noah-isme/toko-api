package shipping

import (
	"context"
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

// RajaOngkirClient calls the RajaOngkir Starter/Basic cost endpoint. The base
// URL is configurable because RajaOngkir has multiple product tiers and the
// newer Komerce endpoint is not wire-compatible with Starter.
type RajaOngkirClient struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

func (c RajaOngkirClient) Rates(ctx context.Context, r RateReq) ([]Rate, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, errors.New("rajaongkir api key is required")
	}
	if strings.TrimSpace(r.Origin) == "" || strings.TrimSpace(r.Destination) == "" {
		return nil, errors.New("shipping origin and destination are required")
	}
	weight := r.WeightGram
	if weight <= 0 {
		weight = 1
	}
	courier := strings.TrimSpace(strings.ToLower(r.Courier))
	if courier == "" {
		courier = "jne"
	}
	form := url.Values{"origin": {r.Origin}, "destination": {r.Destination}, "weight": {strconv.Itoa(weight)}, "courier": {courier}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.endpoint(), "/")+"/cost", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("key", strings.TrimSpace(c.APIKey))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := c.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("rajaongkir quote: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("rajaongkir quote: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		RajaOngkir struct {
			Status struct {
				Code        int    `json:"code"`
				Description string `json:"description"`
			} `json:"status"`
			Results []struct {
				Costs []struct {
					Service     string `json:"service"`
					Description string `json:"description"`
					Cost        []struct {
						Value int64  `json:"value"`
						ETD   string `json:"etd"`
					} `json:"cost"`
				} `json:"costs"`
			} `json:"results"`
		} `json:"rajaongkir"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode rajaongkir quote: %w", err)
	}
	if result.RajaOngkir.Status.Code != 0 && result.RajaOngkir.Status.Code != 200 {
		return nil, errors.New(result.RajaOngkir.Status.Description)
	}
	if len(result.RajaOngkir.Results) == 0 {
		return nil, errors.New("rajaongkir returned no courier result")
	}
	rates := make([]Rate, 0, len(result.RajaOngkir.Results[0].Costs))
	for _, cost := range result.RajaOngkir.Results[0].Costs {
		if len(cost.Cost) == 0 {
			continue
		}
		rates = append(rates, Rate{Service: cost.Service, Price: cost.Cost[0].Value, ETD: cost.Cost[0].ETD, Courier: courier})
	}
	if len(rates) == 0 {
		return nil, errors.New("rajaongkir returned no shipping services")
	}
	return rates, nil
}

func (c RajaOngkirClient) endpoint() string {
	host := strings.TrimSpace(c.BaseURL)
	if host == "" {
		return "https://api.rajaongkir.com/starter"
	}
	return host
}
func (c RajaOngkirClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
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
