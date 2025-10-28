package security

import (
	"net/http"
	"strconv"
	"strings"
)

// Headers configures common security headers for HTTP responses.
type Headers struct {
	Enable                bool
	EnableHSTS            bool
	HSTSMaxAge            int
	HSTSIncludeSubdomains bool
}

// Middleware attaches standard security headers to each response.
func (h Headers) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.Enable {
			next.ServeHTTP(w, r)
			return
		}
		headers := w.Header()
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		headers.Set("Referrer-Policy", "no-referrer")
		headers.Set("Permissions-Policy", "geolocation=(), microphone=()")
		if h.EnableHSTS && r.TLS != nil {
			maxAge := h.HSTSMaxAge
			if maxAge <= 0 {
				maxAge = 31536000
			}
			value := "max-age=" + strconv.Itoa(maxAge)
			if h.HSTSIncludeSubdomains {
				value += "; includeSubDomains"
			}
			headers.Set("Strict-Transport-Security", value)
		}
		next.ServeHTTP(w, r)
	})
}

// AllowCORS returns middleware enforcing an allowlist of origins.
func AllowCORS(originsCSV string) func(http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	wildcard := false
	for _, origin := range strings.Split(originsCSV, ",") {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			continue
		}
		if trimmed == "*" {
			wildcard = true
			continue
		}
		allowed[trimmed] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			allowOrigin := false
			if origin != "" {
				if wildcard {
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.Header().Del("Access-Control-Allow-Credentials")
					allowOrigin = true
				} else if _, ok := allowed[origin]; ok {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					allowOrigin = true
				}
				if allowOrigin {
					w.Header().Add("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, X-CSRF-Token, X-Request-ID, X-Idempotency-Key")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Expose-Headers", "Link, X-Request-ID")
				}
			}

			if r.Method == http.MethodOptions {
				if allowOrigin || origin == "" && wildcard {
					w.WriteHeader(http.StatusNoContent)
				} else {
					http.Error(w, "cors origin not allowed", http.StatusForbidden)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
