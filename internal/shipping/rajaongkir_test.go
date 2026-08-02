package shipping

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type rateRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rateRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRajaOngkirRatesUsesAuthenticatedCostEndpoint(t *testing.T) {
	client := &http.Client{Transport: rateRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/cost" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("key") != "api-key" {
			t.Fatalf("key = %q", r.Header.Get("key"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"rajaongkir":{"status":{"code":200,"description":"OK"},"results":[{"costs":[{"service":"REG","cost":[{"value":15000,"etd":"2-3"}]}]}]}}`))}, nil
	})}
	rates, err := (RajaOngkirClient{APIKey: "api-key", BaseURL: "https://provider.test", HTTPClient: client}).Rates(context.Background(), RateReq{Origin: "1", Destination: "2", WeightGram: 1000, Courier: "jne"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rates) != 1 || rates[0].Price != 15000 || rates[0].Service != "REG" {
		t.Fatalf("unexpected rates: %+v", rates)
	}
}
