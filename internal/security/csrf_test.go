package security

import (
	"net/http"
	"net/http/httptest"
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
