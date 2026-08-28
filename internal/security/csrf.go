package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// CSRF protects cookie-based flows using the double-submit technique.
type CSRF struct {
	Header         string
	CookieDomain   string
	CookiePath     string
	CookieSecure   bool
	CookieSameSite http.SameSite
	CookieMaxAge   int
}

// Middleware enforces that non-idempotent requests include a CSRF token header matching a cookie.
func (c CSRF) Middleware(next http.Handler) http.Handler {
	headerName := strings.TrimSpace(c.Header)
	if headerName == "" {
		headerName = "X-CSRF-Token"
	}
	cookiePath := strings.TrimSpace(c.CookiePath)
	if cookiePath == "" {
		cookiePath = "/"
	}
	cookieSameSite := c.CookieSameSite
	if cookieSameSite == 0 || cookieSameSite == http.SameSiteDefaultMode {
		cookieSameSite = http.SameSiteLaxMode
	}
	cookieMaxAge := c.CookieMaxAge
	if cookieMaxAge <= 0 {
		cookieMaxAge = 86400
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isInboundWebhook(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		method := r.Method
		if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions || method == http.MethodTrace {
			if _, err := r.Cookie(headerName); err != nil {
				token, err := newCSRFToken()
				if err != nil {
					http.Error(w, "unable to initialize csrf token", http.StatusInternalServerError)
					return
				}
				http.SetCookie(w, &http.Cookie{
					Name:     headerName,
					Value:    token,
					Path:     cookiePath,
					Domain:   strings.TrimSpace(c.CookieDomain),
					MaxAge:   cookieMaxAge,
					Secure:   c.CookieSecure,
					HttpOnly: false,
					SameSite: cookieSameSite,
				})
			}
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

func isInboundWebhook(path string) bool {
	const webhookPrefix = "/api/v1/webhooks/"
	return path == strings.TrimSuffix(webhookPrefix, "/") || strings.HasPrefix(path, webhookPrefix)
}

func newCSRFToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(token[:]), nil
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
