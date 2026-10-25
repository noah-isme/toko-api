package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/admin"
	"github.com/noah-isme/backend-toko/internal/security"
)

func noop(http.ResponseWriter, *http.Request) {}

// adminRouterLikeMain nests the import route the way main() does
// (r.Route("/api/v1") -> v.Route("/admin") -> admin.Post("/products/import")).
func adminRouterLikeMain(importSubpath string) chi.Router {
	r := chi.NewRouter()
	r.Route("/api/v1", func(v chi.Router) {
		v.Route("/admin", func(a chi.Router) {
			a.Use(func(next http.Handler) http.Handler { return next })
			a.Get("/products", noop)
			a.Post(importSubpath, noop)
			a.Get("/products/{id}", noop)
		})
	})
	return r
}

func TestCheckBodyLimitOverridesMatchesImportRoute(t *testing.T) {
	r := adminRouterLikeMain("/products/import")
	if err := checkBodyLimitOverrides(r, admin.BodyLimitOverrides()); err != nil {
		t.Fatalf("import override should match the mounted route: %v", err)
	}
}

func TestCheckBodyLimitOverridesReportsDrift(t *testing.T) {
	r := adminRouterLikeMain("/catalog/import")
	err := checkBodyLimitOverrides(r, admin.BodyLimitOverrides())
	if err == nil || !strings.Contains(err.Error(), admin.ImportRoutePath) {
		t.Fatalf("expected drift error naming %s, got %v", admin.ImportRoutePath, err)
	}
	// Method matters too: a GET-only route does not satisfy a POST override.
	err = checkBodyLimitOverrides(adminRouterLikeMain("/products/import"), []security.BodyLimitOverride{{Method: http.MethodPut, Path: admin.ImportRoutePath, Max: 1}})
	if err == nil {
		t.Fatal("expected a method mismatch to be reported")
	}
}
