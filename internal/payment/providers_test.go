package payment

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestMidtransCreateIntentUsesSnapAPI(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/snap/v1/transactions" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("server-key:"))
		if r.Header.Get("Authorization") != wantAuth {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		return jsonResponse(`{"token":"snap-token","redirect_url":"https://pay.example/snap-token"}`), nil
	})}
	result, err := (Midtrans{ServerKey: "server-key", BaseURL: "https://provider.test", HTTPClient: client}).CreateIntent(context.Background(), IntentRequest{OrderID: "order-1", Amount: 12500, ExpiresAtSec: 900})
	if err != nil {
		t.Fatal(err)
	}
	if result.Token != "snap-token" || result.RedirectURL != "https://pay.example/snap-token" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMidtransRefundUsesIdempotentSnapPayload(t *testing.T) {
	var payloads []string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/order-1/refund" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, string(body))
		if r.Header.Get("transaction-source") != "SNAP_API" {
			t.Fatalf("transaction-source = %q", r.Header.Get("transaction-source"))
		}
		return jsonResponse(`{"refund_key":"rf-abc-0","status_code":"200","transaction_status":"partial_refund"}`), nil
	})}
	m := Midtrans{ServerKey: "server-key", BaseURL: "https://provider.test", HTTPClient: client}
	res, err := m.Refund(context.Background(), RefundRequest{OrderID: "order-1", RefundKey: "rf-abc-0", Amount: 12500, Reason: "customer return"})
	if err != nil || res.ProviderRef != "rf-abc-0" || res.Status != RefundStatusSucceeded {
		t.Fatalf("refund = %+v err=%v", res, err)
	}
	if !strings.Contains(payloads[0], `"refund_key":"rf-abc-0"`) || !strings.Contains(payloads[0], `"amount":12500`) {
		t.Fatalf("unexpected refund payload: %s", payloads[0])
	}
	if _, err := m.Refund(context.Background(), RefundRequest{OrderID: "order-1", RefundKey: "rf-abc-1", Amount: 12500, Reason: "customer return"}); err != nil {
		t.Fatal(err)
	}
	if payloads[0] == payloads[1] || !strings.Contains(payloads[1], `"refund_key":"rf-abc-1"`) {
		t.Fatalf("different keys must produce different payloads: %v", payloads)
	}
}

func TestXenditCreateIntentUsesInvoiceAPI(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v2/invoices" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Fatal("missing basic auth")
		}
		return jsonResponse(`{"id":"invoice-1","invoice_url":"https://invoice.example/1"}`), nil
	})}
	result, err := (Xendit{SecretKey: "xnd-key", BaseURL: "https://provider.test", HTTPClient: client}).CreateIntent(context.Background(), IntentRequest{OrderID: "order-2", Amount: 9900, ExpiresAtSec: 900})
	if err != nil {
		t.Fatal(err)
	}
	if result.Token != "invoice-1" || result.RedirectURL != "https://invoice.example/1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestXenditWebhookUsesCallbackToken(t *testing.T) {
	body := []byte(`{"external_id":"order-3","amount":1000,"status":"PAID"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))
	req.Header.Set("x-callback-token", "callback-token")
	result, err := (Xendit{CallbackToken: "callback-token"}).VerifyWebhook(req, body)
	if err != nil || !result.Valid || result.OrderID != "order-3" || result.Status != "PAID" {
		t.Fatalf("unexpected webhook result: %+v err=%v", result, err)
	}
}
