package shipping

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fixtureTracker(t *testing.T, name string) RajaOngkirTracker {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "rajaongkir", name))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/waybill" || r.Header.Get("key") != "k" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return RajaOngkirTracker{APIKey: "k", BaseURL: srv.URL, HTTPClient: srv.Client()}
}

func wib() *time.Location { return time.FixedZone("WIB", 7*3600) }

func TestRajaOngkirTrackParsesWIB(t *testing.T) {
	events, err := fixtureTracker(t, "in_transit.json").Track(context.Background(), TrackReq{Courier: "JNE", TrackingNumber: "X1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %+v", events)
	}
	if want := time.Date(2025, 1, 2, 10, 0, 0, 0, wib()).Unix(); events[0].OccurredAt != want {
		t.Fatalf("OccurredAt = %d, want %d (WIB)", events[0].OccurredAt, want)
	}
	// HH:MM times are accepted too.
	if want := time.Date(2025, 1, 3, 8, 15, 0, 0, wib()).Unix(); events[1].OccurredAt != want {
		t.Fatalf("OccurredAt = %d, want %d", events[1].OccurredAt, want)
	}
	if events[1].Status != "SHIPPED" || events[2].Status != "OUT_FOR_DELIVERY" {
		t.Fatalf("unexpected statuses: %+v", events)
	}
}

func TestRajaOngkirClassifierWholeWords(t *testing.T) {
	cases := map[string]string{
		"UNDELIVERED":                     "SHIPPED",
		"ROUTE":                           "SHIPPED",
		"OUTBOUND":                        "SHIPPED",
		"SHIPMENT WITHOUT DOCUMENTS":      "SHIPPED",
		"Paket telah diterima":            "SHIPPED",
		"DELIVERED TO [BUDI]":             "SHIPPED",
		"WITH DELIVERY COURIER":           "OUT_FOR_DELIVERY",
		"Sedang diantar ke alamat tujuan": "OUT_FOR_DELIVERY",
		"Out  for delivery":               "OUT_FOR_DELIVERY",
		"Paket dalam pengantaran":         "OUT_FOR_DELIVERY",
	}
	for in, want := range cases {
		if got := classifyManifest("", in); got != want {
			t.Errorf("classifyManifest(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRajaOngkirDeliveredFromDeliveryStatus(t *testing.T) {
	res, err := fixtureTracker(t, "delivered.json").TrackWithSummary(context.Background(), TrackReq{Courier: "jne", TrackingNumber: "X1"})
	if err != nil {
		t.Fatal(err)
	}
	delivered := 0
	for _, ev := range res.Events {
		if ev.Status == "DELIVERED" {
			delivered++
			if want := time.Date(2025, 1, 5, 14, 20, 0, 0, wib()).Unix(); ev.OccurredAt != want {
				t.Fatalf("DELIVERED at %d, want POD time %d", ev.OccurredAt, want)
			}
			if ev.Description != "Diterima oleh BUDI" {
				t.Fatalf("description = %q", ev.Description)
			}
		}
	}
	if delivered != 1 || res.Terminal != "" {
		t.Fatalf("want exactly one DELIVERED and no terminal, got %+v", res)
	}
}

func TestRajaOngkirNotDeliveredWhenFlagFalse(t *testing.T) {
	res, err := fixtureTracker(t, "in_transit.json").TrackWithSummary(context.Background(), TrackReq{Courier: "jne", TrackingNumber: "X1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range res.Events {
		if ev.Status == "DELIVERED" {
			t.Fatalf("manifest text must not yield DELIVERED: %+v", res.Events)
		}
	}
}

func TestRajaOngkirReturnedIsTerminal(t *testing.T) {
	res, err := fixtureTracker(t, "returned.json").TrackWithSummary(context.Background(), TrackReq{Courier: "jne", TrackingNumber: "X1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Terminal != "RETURNED" {
		t.Fatalf("Terminal = %q, want RETURNED", res.Terminal)
	}
	if terminalStatus("UNDELIVERED") != "" || terminalStatus("Paket hilang") != "LOST" {
		t.Fatal("terminalStatus word matching is wrong")
	}
}

func TestRajaOngkirTrackErrorsWithoutKeyOrOnHTTPError(t *testing.T) {
	if _, err := (RajaOngkirTracker{}).Track(context.Background(), TrackReq{}); err == nil {
		t.Fatal("want error without api key")
	}
	tr := fixtureTracker(t, "in_transit.json")
	tr.APIKey = "wrong"
	if _, err := tr.Track(context.Background(), TrackReq{Courier: "jne", TrackingNumber: "X1"}); err == nil {
		t.Fatal("want error on non-2xx")
	}
}
