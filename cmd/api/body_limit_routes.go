package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/security"
)

// checkBodyLimitOverrides verifies that every BodyLimit override names a route
// the router actually serves. Overrides match the raw request path before
// routing, so a renamed or re-mounted route would otherwise silently fall back
// to the global cap (e.g. CSV imports over 1 MiB returning 413).
func checkBodyLimitOverrides(r chi.Routes, overrides []security.BodyLimitOverride) error {
	registered := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+normalizeRoutePattern(route)] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk routes: %w", err)
	}
	var missing []string
	for _, o := range overrides {
		key := strings.ToUpper(o.Method) + " " + normalizeRoutePattern(o.Path)
		if !registered[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("body limit overrides do not match any route: %s", strings.Join(missing, ", "))
	}
	return nil
}

// normalizeRoutePattern drops the "/*" chi leaves on mounted sub-router
// patterns and any trailing slash, so "/api/v1/*/admin/x/" style artefacts
// compare equal to the plain path.
func normalizeRoutePattern(p string) string {
	p = strings.ReplaceAll(p, "/*/", "/")
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}
