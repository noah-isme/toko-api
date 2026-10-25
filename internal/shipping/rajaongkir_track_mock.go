package shipping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

// Track implements Provider.
func (t RajaOngkirTracker) Track(ctx context.Context, req TrackReq) ([]TrackEvent, error) {
	res, err := t.TrackWithSummary(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Events, nil
}

// TrackWithSummary fetches the waybill and also reports whether the carrier
// considers the parcel returned or lost (TrackResult.Terminal).
//
// Timestamps are WIB wall-clock values and are parsed in Asia/Jakarta.
// DELIVERED comes only from the provider's own delivered flag or
// delivery/summary status, never from manifest text (which contains words
// like "UNDELIVERED"); manifest entries are classified as SHIPPED or, on a
// whole-phrase match, OUT_FOR_DELIVERY.
func (t RajaOngkirTracker) TrackWithSummary(ctx context.Context, req TrackReq) (TrackResult, error) {
	if strings.TrimSpace(t.APIKey) == "" {
		return TrackResult{}, errors.New("rajaongkir api key is required")
	}
	form := url.Values{"waybill": {strings.TrimSpace(req.TrackingNumber)}, "courier": {strings.TrimSpace(strings.ToLower(req.Courier))}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.endpoint(), "/")+"/waybill", strings.NewReader(form.Encode()))
	if err != nil {
		return TrackResult{}, err
	}
	request.Header.Set("key", strings.TrimSpace(t.APIKey))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := t.client().Do(request)
	if err != nil {
		return TrackResult{}, fmt.Errorf("rajaongkir tracking: %w", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return TrackResult{}, fmt.Errorf("read rajaongkir tracking response: %w", readErr)
	}
	if closeErr != nil {
		return TrackResult{}, fmt.Errorf("close rajaongkir tracking response: %w", closeErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return TrackResult{}, fmt.Errorf("rajaongkir tracking: status %d", response.StatusCode)
	}
	return parseRajaOngkirWaybill(body)
}

type rajaOngkirWaybill struct {
	RajaOngkir struct {
		Result struct {
			Delivered bool `json:"delivered"`
			Summary   struct {
				Status string `json:"status"`
			} `json:"summary"`
			DeliveryStatus struct {
				Status      string `json:"status"`
				PODReceiver string `json:"pod_receiver"`
				PODDate     string `json:"pod_date"`
				PODTime     string `json:"pod_time"`
			} `json:"delivery_status"`
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

func parseRajaOngkirWaybill(body []byte) (TrackResult, error) {
	var payload rajaOngkirWaybill
	if err := json.Unmarshal(body, &payload); err != nil {
		return TrackResult{}, fmt.Errorf("decode rajaongkir tracking: %w", err)
	}
	result := payload.RajaOngkir.Result
	events := make([]TrackEvent, 0, len(result.Manifest)+1)
	var lastManifest int64
	for _, m := range result.Manifest {
		occurredAt := parseWIB(m.ManifestDate, m.ManifestTime)
		if occurredAt > lastManifest {
			lastManifest = occurredAt
		}
		events = append(events, TrackEvent{
			Status:      classifyManifest(m.ManifestCode, m.ManifestDescription),
			Description: strings.TrimSpace(m.ManifestDescription),
			Location:    strings.TrimSpace(m.CityName),
			OccurredAt:  occurredAt,
		})
	}
	if result.Delivered || isDeliveredWord(result.DeliveryStatus.Status) || isDeliveredWord(result.Summary.Status) {
		description := "Paket telah diterima"
		if receiver := strings.TrimSpace(result.DeliveryStatus.PODReceiver); receiver != "" {
			description = "Diterima oleh " + receiver
		}
		occurredAt := parseWIB(result.DeliveryStatus.PODDate, result.DeliveryStatus.PODTime)
		if occurredAt == 0 {
			occurredAt = lastManifest
		}
		events = append(events, TrackEvent{Status: "DELIVERED", Description: description, OccurredAt: occurredAt})
	}
	terminal := terminalStatus(result.DeliveryStatus.Status)
	if terminal == "" {
		terminal = terminalStatus(result.Summary.Status)
	}
	if len(events) == 0 && terminal == "" {
		return TrackResult{}, errors.New("rajaongkir returned no tracking events")
	}
	return TrackResult{Events: events, Terminal: terminal}, nil
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

var (
	outForDeliveryPattern = regexp.MustCompile(`\b(out for delivery|with delivery courier|delivery courier|sedang diantar|dalam pengantaran|diantar ke|kurir pengantar)\b`)
	deliveredPattern      = regexp.MustCompile(`^\s*(delivered|terkirim|diterima)\s*$`)
	returnedPattern       = regexp.MustCompile(`\b(return|returned|retur|dikembalikan)\b`)
	lostPattern           = regexp.MustCompile(`\b(lost|hilang)\b`)
	wibLocation           = loadWIB()
)

func loadWIB() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*3600)
}

// parseWIB parses a RajaOngkir date and time (WIB wall clock) into Unix
// seconds, or 0 when it cannot be parsed.
func parseWIB(date, clock string) int64 {
	date, clock = strings.TrimSpace(date), strings.TrimSpace(clock)
	if date == "" {
		return 0
	}
	if clock == "" {
		clock = "00:00:00"
	} else if len(clock) == 5 {
		clock += ":00"
	}
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", date+" "+clock, wibLocation)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

// classifyManifest maps manifest text to SHIPPED or OUT_FOR_DELIVERY using
// whole-phrase matches only. Manifest text never yields DELIVERED.
func classifyManifest(code, description string) string {
	value := strings.ToLower(strings.Join(strings.Fields(code+" "+description), " "))
	if outForDeliveryPattern.MatchString(value) {
		return "OUT_FOR_DELIVERY"
	}
	return "SHIPPED"
}

// isDeliveredWord reports whether a provider status field says delivered.
func isDeliveredWord(status string) bool {
	return deliveredPattern.MatchString(strings.ToLower(status))
}

// terminalStatus returns RETURNED or LOST when a provider status field says
// the parcel will not be delivered, else "".
func terminalStatus(status string) string {
	value := strings.ToLower(status)
	switch {
	case returnedPattern.MatchString(value):
		return "RETURNED"
	case lostPattern.MatchString(value):
		return "LOST"
	default:
		return ""
	}
}
