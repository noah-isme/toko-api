package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/noah-isme/backend-toko/internal/common"
)

var errNoToken = errors.New("auth: token missing")

// Middleware wires authentication context into HTTP handlers.
type Middleware struct {
	Service      *Service
	AccessCookie string
}

// Authenticate attaches the user identifier to the request context when a valid token is present.
func (m Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, err := m.authenticateRequest(r)
		if err != nil && !errors.Is(err, errNoToken) {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth enforces that a valid token is present before executing the next handler.
func (m Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, err := m.authenticateRequest(r)
		if err != nil {
			if errors.Is(err, errNoToken) {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
				return
			}
			var appErr *common.AppError
			if errors.As(err, &appErr) {
				status := appErr.HTTPStatus
				if status == 0 {
					status = http.StatusUnauthorized
				}
				common.JSONError(w, status, appErr.Code, appErr.Message, appErr.Details)
				return
			}
			common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m Middleware) authenticateRequest(r *http.Request) (context.Context, error) {
	if m.Service == nil {
		return r.Context(), errors.New("auth: service not configured")
	}
	token := m.extractToken(r)
	if token == "" {
		return r.Context(), errNoToken
	}
	userID, err := m.Service.ParseAccessToken(token)
	if err != nil {
		return r.Context(), err
	}
	return common.WithUserID(r.Context(), userID), nil
}

func (m Middleware) extractToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	if m.AccessCookie != "" {
		if cookie, err := r.Cookie(m.AccessCookie); err == nil {
			if value := strings.TrimSpace(cookie.Value); value != "" {
				return value
			}
		}
	}
	return ""
}
