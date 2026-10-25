package payment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func midtransWith(t *testing.T, status int, body string) (Midtrans, *int32) {
	t.Helper()
	var calls int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	return Midtrans{ServerKey: "k", BaseURL: "https://provider.test", HTTPClient: client}, &calls
}

var midtransReq = RefundRequest{OrderID: "order-1", RefundKey: "rf-k-0", Amount: 1000, Reason: "r"}

func TestMidtransRefundBodyStatus412IsDefiniteError(t *testing.T) {
	m, _ := midtransWith(t, 200, `{"status_code":"412","status_message":"Merchant cannot modify the status of the transaction"}`)
	res, err := m.Refund(context.Background(), midtransReq)
	if err == nil || !IsDefiniteRefundFailure(err) {
		t.Fatalf("want definite error, got %v", err)
	}
	if !strings.Contains(err.Error(), "412") || !strings.Contains(err.Error(), "Merchant cannot modify") {
		t.Fatalf("error = %v", err)
	}
	if res != (RefundResult{}) {
		t.Fatalf("result must be zero on error: %+v", res)
	}
}

func TestMidtransRefundBodyStatus202IsDefiniteDenial(t *testing.T) {
	// 202 in Midtrans' status-code table means denied by the bank or fraud detection.
	m, _ := midtransWith(t, 200, `{"refund_key":"rf-k-0","status_code":"202","status_message":"Deny by Bank"}`)
	res, err := m.Refund(context.Background(), midtransReq)
	if !IsDefiniteRefundFailure(err) || res.ProviderRef != "" {
		t.Fatalf("want definite denial without ref, got %+v %v", res, err)
	}
}

func TestMidtransRefundBodyStatus201IsIndeterminate(t *testing.T) {
	m, _ := midtransWith(t, 200, `{"refund_key":"rf-k-0","status_code":"201"}`)
	res, err := m.Refund(context.Background(), midtransReq)
	if err == nil || IsDefiniteRefundFailure(err) || res.ProviderRef != "" {
		t.Fatalf("201 must be indeterminate, got %+v %v", res, err)
	}
}

func TestMidtransRefund200WithNonRefundTransactionStatusIsIndeterminate(t *testing.T) {
	m, _ := midtransWith(t, 200, `{"refund_key":"rf-k-0","status_code":"200","transaction_status":"settlement"}`)
	if _, err := m.Refund(context.Background(), midtransReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("want indeterminate, got %v", err)
	}
	m, _ = midtransWith(t, 200, `{"status_code":"200","transaction_status":"refund"}`)
	res, err := m.Refund(context.Background(), midtransReq)
	if err != nil || res.ProviderRef != "rf-k-0" || res.Status != RefundStatusSucceeded {
		t.Fatalf("missing refund_key falls back to request key on success: %+v %v", res, err)
	}
}

func TestMidtransRefundMissingStatusCodeIsError(t *testing.T) {
	m, _ := midtransWith(t, 200, `{"refund_key":"k"}`)
	if _, err := m.Refund(context.Background(), midtransReq); !IsDefiniteRefundFailure(err) {
		t.Fatalf("missing status_code must be definite, got %v", err)
	}
	m, _ = midtransWith(t, 200, `not json`)
	if _, err := m.Refund(context.Background(), midtransReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("unparsable body must be indeterminate, got %v", err)
	}
}

func TestMidtransRefundDuplicateKeyIsIndeterminate(t *testing.T) {
	m, _ := midtransWith(t, 200, `{"status_code":"406","status_message":"Duplicate refund_key"}`)
	if _, err := m.Refund(context.Background(), midtransReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("duplicate key must stay indeterminate, got %v", err)
	}
}

func TestMidtransRefundHTTP5xxIsIndeterminate(t *testing.T) {
	m, _ := midtransWith(t, 503, `unavailable`)
	if _, err := m.Refund(context.Background(), midtransReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("5xx must be indeterminate, got %v", err)
	}
	m, _ = midtransWith(t, 400, `{"status_code":"400","status_message":"bad amount"}`)
	_, err := m.Refund(context.Background(), midtransReq)
	if !IsDefiniteRefundFailure(err) || RefundErrorCode(err) != "400" {
		t.Fatalf("4xx must be definite, got %v", err)
	}
}

func TestMidtransRefundBodyStatus5xxIsIndeterminate(t *testing.T) {
	// Midtrans often answers HTTP 200 with the real code in the body. A server-class
	// body code may hide an applied refund, so the key must never be rotated on it.
	bodies := map[string]string{
		"500": `{"status_code":"500","status_message":"Internal Server Error"}`,
		"502": `{"status_code":"502","status_message":"Internal Server Error: Connection to the bank failed"}`,
		"503": `{"status_code":"503","status_message":"Service Unavailable"}`,
		"504": `{"status_code":"504","status_message":"Gateway Timeout"}`,
	}
	for code, body := range bodies {
		for _, httpStatus := range []int{200, 400} {
			m, _ := midtransWith(t, httpStatus, body)
			res, err := m.Refund(context.Background(), midtransReq)
			if err == nil || IsDefiniteRefundFailure(err) || res.ProviderRef != "" {
				t.Fatalf("body %s (HTTP %d) must be indeterminate, got %+v %v", code, httpStatus, res, err)
			}
			if RefundErrorCode(err) != code {
				t.Fatalf("body %s: code = %q", code, RefundErrorCode(err))
			}
		}
	}
}

func TestMidtransRefundTruncatesLargeErrorBody(t *testing.T) {
	m, _ := midtransWith(t, 502, "<html>"+strings.Repeat("x", 100000)+"</html>")
	_, err := m.Refund(context.Background(), midtransReq)
	if err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("want indeterminate, got %v", err)
	}
	if len(err.Error()) > 500 {
		t.Fatalf("error message not truncated: %d bytes", len(err.Error()))
	}
}

func TestMidtransRefundTransportErrorIsIndeterminate(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	_, err := (Midtrans{ServerKey: "k", BaseURL: "https://provider.test", HTTPClient: client}).Refund(context.Background(), midtransReq)
	if err == nil || IsDefiniteRefundFailure(err) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transport error must be indeterminate, got %v", err)
	}
}

func TestMidtransRefundRequiresKeyAndAmount(t *testing.T) {
	m, calls := midtransWith(t, 200, `{"status_code":"200"}`)
	for _, req := range []RefundRequest{{OrderID: "o", Amount: 1}, {OrderID: "o", RefundKey: "k"}} {
		if _, err := m.Refund(context.Background(), req); !IsDefiniteRefundFailure(err) {
			t.Fatalf("want definite validation error for %+v, got %v", req, err)
		}
	}
	if *calls != 0 {
		t.Fatalf("validation must not call the gateway, calls=%d", *calls)
	}
	res, err := (Midtrans{Stub: true}).Refund(context.Background(), midtransReq)
	if err != nil || res.ProviderRef != "stub-refund-rf-k-0" || res.Status != RefundStatusSucceeded {
		t.Fatalf("stub = %+v %v", res, err)
	}
}

// midtransStatusFixture mirrors the refunds[] shape of Midtrans' GET /v2/{order}/status.
const midtransStatusFixture = `{"status_code":"200","transaction_status":"partial_refund","order_id":"order-1","refund_amount":"1000.00",
"refunds":[{"refund_chargeback_id":1,"refund_chargeback_uuid":"u-1","refund_amount":"1000.00","created_at":"2026-10-18 10:00:00","reason":"r","refund_key":"rf-x-0","refund_method":""}]}`

func TestMidtransLookupRefundFindsKey(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v2/order-1/status" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(midtransStatusFixture), nil
	})}
	m := Midtrans{ServerKey: "k", BaseURL: "https://provider.test", HTTPClient: client}
	res, found, err := m.LookupRefund(context.Background(), RefundRequest{OrderID: "order-1", RefundKey: "rf-x-0"})
	if err != nil || !found || res.ProviderRef != "rf-x-0" || res.Status != RefundStatusSucceeded {
		t.Fatalf("lookup = %+v found=%v err=%v", res, found, err)
	}
	if _, found, err := m.LookupRefund(context.Background(), RefundRequest{OrderID: "order-1", RefundKey: "rf-x-1"}); err != nil || found {
		t.Fatalf("other key: found=%v err=%v", found, err)
	}
	m5, _ := midtransWith(t, 500, `boom`)
	if _, found, err := m5.LookupRefund(context.Background(), RefundRequest{OrderID: "order-1", RefundKey: "rf-x-0"}); err == nil || found || IsDefiniteRefundFailure(err) {
		t.Fatalf("5xx lookup must be an indeterminate error: found=%v err=%v", found, err)
	}
	m404, _ := midtransWith(t, 200, `{"status_code":"404","status_message":"Transaction doesn't exist."}`)
	if _, found, err := m404.LookupRefund(context.Background(), RefundRequest{OrderID: "order-1", RefundKey: "rf-x-0"}); err != nil || found {
		t.Fatalf("404 lookup: found=%v err=%v", found, err)
	}
}

type xenditCapture struct {
	method, path, auth, idem, query string
	body                            map[string]any
	raw                             string
}

func xenditServer(t *testing.T, status int, reply string) (*httptest.Server, *[]xenditCapture) {
	t.Helper()
	var captured []xenditCapture
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := xenditCapture{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"), idem: r.Header.Get("idempotency-key"), query: r.URL.RawQuery, raw: string(raw)}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &c.body)
		}
		captured = append(captured, c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

var xenditReq = RefundRequest{OrderID: "order-2", PaymentRef: "inv-1", RefundKey: "rf-y-0", Amount: 5000, Reason: "damaged item"}

func TestXenditRefundUsesRefundsAPI(t *testing.T) {
	srv, captured := xenditServer(t, 200, `{"id":"rfd-1","status":"SUCCEEDED","reference_id":"rf-y-0"}`)
	x := Xendit{SecretKey: "xnd", BaseURL: srv.URL}
	res, err := x.Refund(context.Background(), xenditReq)
	if err != nil || res != (RefundResult{ProviderRef: "rfd-1", Status: RefundStatusSucceeded}) {
		t.Fatalf("refund = %+v err=%v", res, err)
	}
	c := (*captured)[0]
	if c.method != http.MethodPost || c.path != "/refunds" {
		t.Fatalf("request %s %s", c.method, c.path)
	}
	if c.auth != "Basic "+base64.StdEncoding.EncodeToString([]byte("xnd:")) || c.idem != "rf-y-0" {
		t.Fatalf("auth=%q idem=%q", c.auth, c.idem)
	}
	if c.body["invoice_id"] != "inv-1" || c.body["amount"] != float64(5000) || c.body["currency"] != "IDR" ||
		c.body["reason"] != "REQUESTED_BY_CUSTOMER" || c.body["reference_id"] != "rf-y-0" {
		t.Fatalf("body = %v", c.body)
	}
	if _, ok := c.body["payment_request_id"]; ok {
		t.Fatalf("invoice refunds must not send payment_request_id: %v", c.body)
	}
	if meta, _ := c.body["metadata"].(map[string]any); meta["note"] != "damaged item" {
		t.Fatalf("metadata = %v", c.body["metadata"])
	}
	// Same request, same key: byte-identical payload (Xendit rejects a reused key with a different body).
	if _, err := x.Refund(context.Background(), xenditReq); err != nil {
		t.Fatal(err)
	}
	if (*captured)[1].raw != c.raw {
		t.Fatalf("retry payload differs:\n%s\n%s", c.raw, (*captured)[1].raw)
	}
}

func TestXenditRefundUsesPaymentRequestIDWhenPrefixed(t *testing.T) {
	srv, captured := xenditServer(t, 200, `{"id":"rfd-1","status":"SUCCEEDED"}`)
	req := xenditReq
	req.PaymentRef = "pr-123"
	if _, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	body := (*captured)[0].body
	if body["payment_request_id"] != "pr-123" {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["invoice_id"]; ok {
		t.Fatalf("payment request refunds must not send invoice_id: %v", body)
	}
}

func TestXenditRefundPendingStatus(t *testing.T) {
	srv, _ := xenditServer(t, 200, `{"id":"rfd-2","status":"PENDING"}`)
	res, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), xenditReq)
	if err != nil || res != (RefundResult{ProviderRef: "rfd-2", Status: RefundStatusPending}) {
		t.Fatalf("refund = %+v err=%v", res, err)
	}
}

func TestXenditRefundGatewayError(t *testing.T) {
	srv, _ := xenditServer(t, 400, `{"error_code":"REFUND_AMOUNT_EXCEEDED","message":"amount exceeds"}`)
	_, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), xenditReq)
	if !IsDefiniteRefundFailure(err) || !strings.Contains(err.Error(), "REFUND_AMOUNT_EXCEEDED") {
		t.Fatalf("want definite REFUND_AMOUNT_EXCEEDED, got %v", err)
	}
}

func TestXenditRefundDuplicateIsIndeterminate(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{409, `{"error_code":"DUPLICATE_ERROR","message":"reference_id already used"}`},
		{409, `{"error_code":"REFUND_IN_PROGRESS","message":"refund in progress"}`},
		{400, `{"error_code":"IDEMPOTENCY_ERROR","message":"payload mismatch"}`},
		{409, `conflict`},
		{429, `{"error_code":"RATE_LIMIT_EXCEEDED","message":"slow down"}`},
	}
	for _, tc := range cases {
		srv, _ := xenditServer(t, tc.status, tc.body)
		_, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), xenditReq)
		if err == nil || IsDefiniteRefundFailure(err) {
			t.Fatalf("%d %s must be indeterminate, got %v", tc.status, tc.body, err)
		}
	}
}

func TestXenditRefundFailedStatusIsError(t *testing.T) {
	srv, _ := xenditServer(t, 200, `{"id":"rfd-3","status":"FAILED","failure_code":"INSUFFICIENT_BALANCE"}`)
	_, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), xenditReq)
	if !IsDefiniteRefundFailure(err) || !strings.Contains(err.Error(), "INSUFFICIENT_BALANCE") {
		t.Fatalf("want definite INSUFFICIENT_BALANCE, got %v", err)
	}
}

func TestXenditRefundServerErrorIsIndeterminate(t *testing.T) {
	srv, _ := xenditServer(t, 502, `bad gateway`)
	if _, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), xenditReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("5xx must be indeterminate, got %v", err)
	}
	srv, _ = xenditServer(t, 200, `{"status":"SUCCEEDED"}`)
	if _, err := (Xendit{SecretKey: "xnd", BaseURL: srv.URL}).Refund(context.Background(), xenditReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("missing id must be indeterminate, got %v", err)
	}
	closer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("hijack unsupported")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.(*net.TCPConn).Close()
	}))
	defer closer.Close()
	if _, err := (Xendit{SecretKey: "xnd", BaseURL: closer.URL}).Refund(context.Background(), xenditReq); err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("closed connection must be indeterminate, got %v", err)
	}
}

func TestXenditRefundStubAndValidation(t *testing.T) {
	res, err := (Xendit{Stub: true}).Refund(context.Background(), xenditReq)
	if err != nil || res.ProviderRef != "stub-refund-rf-y-0" || res.Status != RefundStatusSucceeded {
		t.Fatalf("stub = %+v %v", res, err)
	}
	srv, captured := xenditServer(t, 200, `{"id":"rfd","status":"SUCCEEDED"}`)
	noSecret := xenditReq
	noRef := xenditReq
	noRef.PaymentRef = ""
	zero := xenditReq
	zero.Amount = 0
	cases := []struct {
		x   Xendit
		req RefundRequest
	}{
		{Xendit{BaseURL: srv.URL}, noSecret},
		{Xendit{SecretKey: "xnd", BaseURL: srv.URL}, noRef},
		{Xendit{SecretKey: "xnd", BaseURL: srv.URL}, zero},
	}
	for i, tc := range cases {
		if _, err := tc.x.Refund(context.Background(), tc.req); !IsDefiniteRefundFailure(err) {
			t.Fatalf("case %d: want definite validation error, got %v", i, err)
		}
	}
	if len(*captured) != 0 {
		t.Fatalf("validation must not call Xendit, got %d calls", len(*captured))
	}
}

func TestXenditLookupRefund(t *testing.T) {
	srv, captured := xenditServer(t, 200, `{"data":[{"id":"rfd-9","reference_id":"rf-y-0","status":"SUCCEEDED"},{"id":"rfd-8","reference_id":"rf-y-1","status":"FAILED","failure_code":"INSUFFICIENT_BALANCE"}],"has_more":false}`)
	x := Xendit{SecretKey: "xnd", BaseURL: srv.URL}
	res, found, err := x.LookupRefund(context.Background(), xenditReq)
	if err != nil || !found || res != (RefundResult{ProviderRef: "rfd-9", Status: RefundStatusSucceeded}) {
		t.Fatalf("lookup = %+v found=%v err=%v", res, found, err)
	}
	c := (*captured)[0]
	if c.method != http.MethodGet || c.path != "/refunds" || !strings.Contains(c.query, "invoice_id=inv-1") {
		t.Fatalf("request %s %s?%s", c.method, c.path, c.query)
	}
	failed := xenditReq
	failed.RefundKey = "rf-y-1"
	if _, found, err := x.LookupRefund(context.Background(), failed); !found || !IsDefiniteRefundFailure(err) {
		t.Fatalf("failed refund must be found with a definite error: found=%v err=%v", found, err)
	}
	missing := xenditReq
	missing.RefundKey = "rf-y-2"
	if _, found, err := x.LookupRefund(context.Background(), missing); found || err != nil {
		t.Fatalf("missing: found=%v err=%v", found, err)
	}
	more, _ := xenditServer(t, 200, `{"data":[],"has_more":true}`)
	if _, found, err := (Xendit{SecretKey: "xnd", BaseURL: more.URL}).LookupRefund(context.Background(), missing); found || err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("incomplete listing must be indeterminate: found=%v err=%v", found, err)
	}
	down, _ := xenditServer(t, 503, `down`)
	if _, found, err := (Xendit{SecretKey: "xnd", BaseURL: down.URL}).LookupRefund(context.Background(), xenditReq); found || err == nil || IsDefiniteRefundFailure(err) {
		t.Fatalf("5xx lookup must be indeterminate: found=%v err=%v", found, err)
	}
}
