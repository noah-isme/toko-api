package security

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// CSRF protects cookie-based flows using the double-submit technique.
type CSRF struct {
	Header string
}

// Middleware enforces that non-idempotent requests include a CSRF token header matching a cookie.
func (c CSRF) Middleware(next http.Handler) http.Handler {
	headerName := strings.TrimSpace(c.Header)
	if headerName == "" {
		headerName = "X-CSRF-Token"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == http.MethodTrace {
			next.ServeHTTP(w, r)
			return
		}

		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			next.ServeHTTP(w, r)
			return
		}

		token := strings.TrimSpace(r.Header.Get(headerName))
		if token == "" {
			http.Error(w, "missing csrf token", http.StatusForbidden)
			return
		}

		cookie, err := r.Cookie(headerName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			http.Error(w, "missing csrf cookie", http.StatusForbidden)
			return
		}

		if subtleConstantTimeCompare(token, cookie.Value) != 1 {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func subtleConstantTimeCompare(a, b string) int {
	if len(a) != len(b) {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b))
}
