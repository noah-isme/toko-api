package middleware

import (
	"net/http"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

// RequireTenant ensures tenant identifier exists in request context.
func RequireTenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := tenant.From(r.Context()); !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"TENANT_REQUIRED","message":"tenant is required"}}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
