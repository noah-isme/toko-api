package security

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHeadersMiddlewareSetsSecurityHeaders(t *testing.T) {
	middleware := Headers{Enable: true, EnableHSTS: true, HSTSMaxAge: 31536000, HSTSIncludeSubdomains: true}
	handler := middleware.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	headers := rr.Result().Header
	if got := headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff header, got %q", got)
	}
	if got := headers.Get("Strict-Transport-Security"); got == "" {
		t.Fatal("expected hsts header to be set")
	}
}

func TestHeadersMiddlewareDisabled(t *testing.T) {
	middleware := Headers{Enable: false, EnableHSTS: true}
	handler := middleware.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://example.com", nil))
	if rr.Header().Get("X-Content-Type-Options") != "" {
		t.Fatal("expected no security headers when disabled")
	}
}

func TestAllowCORS(t *testing.T) {
	mw := AllowCORS("https://example.com")
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "http://localhost/resource", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for allowed origin, got %d", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Fatalf("unexpected CORS origin header: %q", rr.Header().Get("Access-Control-Allow-Origin"))
	}

	badReq := httptest.NewRequest(http.MethodOptions, "http://localhost/resource", nil)
	badReq.Header.Set("Origin", "https://malicious.example")
	badRR := httptest.NewRecorder()
	handler.ServeHTTP(badRR, badReq)
	if badRR.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for disallowed origin, got %d", badRR.Code)
	}
}
