package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRFMiddlewareBlocksMissingToken(t *testing.T) {
	csrf := CSRF{Header: "X-CSRF-Token"}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

func TestCSRFMiddlewareAllowsValidToken(t *testing.T) {
	csrf := CSRF{Header: "X-CSRF-Token"}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	token := "secure-token"
	req.Header.Set("X-CSRF-Token", token)
	req.AddCookie(&http.Cookie{Name: "X-CSRF-Token", Value: token})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestCSRFMiddlewareBootstrapsCookieOnSafeRequest(t *testing.T) {
	csrf := CSRF{Header: "X-CSRF-Token"}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one bootstrap cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "X-CSRF-Token" {
		t.Fatalf("cookie name = %q, want X-CSRF-Token", cookie.Name)
	}
	if cookie.Value == "" {
		t.Fatal("bootstrap cookie has an empty token")
	}
	if cookie.HttpOnly {
		t.Fatal("bootstrap cookie must be readable by the client")
	}
	if cookie.Path != "/" {
		t.Fatalf("cookie path = %q, want /", cookie.Path)
	}
	if !strings.Contains(rr.Header().Get("Set-Cookie"), "SameSite=Lax") {
		t.Fatalf("Set-Cookie = %q, want SameSite=Lax", rr.Header().Get("Set-Cookie"))
	}
	if cookie.MaxAge != 86400 {
		t.Fatalf("cookie MaxAge = %d, want 86400", cookie.MaxAge)
	}

	post := httptest.NewRequest(http.MethodPost, "/protected", nil)
	post.Header.Set("X-CSRF-Token", cookie.Value)
	post.AddCookie(cookie)
	postRR := httptest.NewRecorder()
	handler.ServeHTTP(postRR, post)
	if postRR.Code != http.StatusOK {
		t.Fatalf("expected bootstrapped token to authorize POST with 200, got %d", postRR.Code)
	}
}

func TestCSRFMiddlewareDoesNotRotateExistingCookie(t *testing.T) {
	csrf := CSRF{Header: "X-CSRF-Token"}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "X-CSRF-Token", Value: "existing-token"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if cookies := rr.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("expected no cookie rotation, got %d Set-Cookie headers", len(cookies))
	}
}

func TestCSRFMiddlewareExemptsInboundWebhooks(t *testing.T) {
	csrf := CSRF{Header: "X-CSRF-Token"}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	for _, path := range []string{
		"/api/v1/webhooks/payment/midtrans",
		"/api/v1/webhooks/shipping/rajaongkir",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted {
			t.Errorf("POST %s got %d, want 202", path, rr.Code)
		}
	}
}

func TestCSRFMiddlewareHonorsCookieAttributes(t *testing.T) {
	csrf := CSRF{
		Header:         "X-CSRF-Token",
		CookieDomain:   "example.com",
		CookiePath:     "/api",
		CookieSecure:   true,
		CookieSameSite: http.SameSiteStrictMode,
		CookieMaxAge:   3600,
	}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Domain != "example.com" || cookie.Path != "/api" || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 3600 {
		t.Fatalf("cookie attributes = domain %q, path %q, secure %t, SameSite %v, MaxAge %d", cookie.Domain, cookie.Path, cookie.Secure, cookie.SameSite, cookie.MaxAge)
	}
}

func TestCSRFMiddlewareSkipsBearer(t *testing.T) {
	csrf := CSRF{Header: "X-CSRF-Token"}
	handler := csrf.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, "/protected", nil)
	req.Header.Set("Authorization", "Bearer abc.def")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for bearer request, got %d", rr.Code)
	}
}
