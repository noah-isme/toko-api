package shipping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

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

// RajaOngkirTracker fetches waybill history from the provider instead of
// manufacturing a shipment event. It shares the same API key and base URL as
// the rate client.
type RajaOngkirTracker struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

func (t RajaOngkirTracker) Track(ctx context.Context, req TrackReq) ([]TrackEvent, error) {
	if strings.TrimSpace(t.APIKey) == "" {
		return nil, errors.New("rajaongkir api key is required")
	}
	form := url.Values{"waybill": {strings.TrimSpace(req.TrackingNumber)}, "courier": {strings.TrimSpace(strings.ToLower(req.Courier))}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.endpoint(), "/")+"/waybill", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("key", strings.TrimSpace(t.APIKey))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := t.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("rajaongkir tracking: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("rajaongkir tracking: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var result struct {
		RajaOngkir struct {
			Result struct {
				Manifest []struct {
					ManifestCode        string `json:"manifest_code"`
					ManifestDescription string `json:"manifest_description"`
					CityName            string `json:"city_name"`
					ManifestDate        string `json:"manifest_date"`
					ManifestTime        string `json:"manifest_time"`
				} `json:"manifest"`
			} `json:"result"`
		} `json:"rajaongkir"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode rajaongkir tracking: %w", err)
	}
	events := make([]TrackEvent, 0, len(result.RajaOngkir.Result.Manifest))
	for _, event := range result.RajaOngkir.Result.Manifest {
		occurredAt := int64(0)
		if parsed, err := time.Parse("2006-01-02 15:04:05", strings.TrimSpace(event.ManifestDate+" "+event.ManifestTime)); err == nil {
			occurredAt = parsed.Unix()
		}
		events = append(events, TrackEvent{Status: normaliseTrackingStatus(event.ManifestCode, event.ManifestDescription), Description: event.ManifestDescription, Location: event.CityName, OccurredAt: occurredAt})
	}
	if len(events) == 0 {
		return nil, errors.New("rajaongkir returned no tracking events")
	}
	return events, nil
}

func (t RajaOngkirTracker) endpoint() string {
	host := strings.TrimSpace(t.BaseURL)
	if host == "" {
		return "https://api.rajaongkir.com/starter"
	}
	return host
}
func (t RajaOngkirTracker) client() *http.Client {
	if t.HTTPClient != nil {
		return t.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}
func normaliseTrackingStatus(code, description string) string {
	value := strings.ToLower(code + " " + description)
	switch {
	case strings.Contains(value, "delivered"):
		return "DELIVERED"
	case strings.Contains(value, "out"):
		return "OUT_FOR_DELIVERY"
	case strings.Contains(value, "received"), strings.Contains(value, "manifest"):
		return "IN_TRANSIT"
	default:
		return "SHIPPED"
	}
}
