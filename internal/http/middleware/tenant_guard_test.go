package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noah-isme/backend-toko/internal/http/middleware"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

func TestRequireTenantMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	handler := middleware.RequireTenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRequireTenantPresent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(tenant.With(req.Context(), "tenant-123"))
	rec := httptest.NewRecorder()
	handler := middleware.RequireTenant(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
