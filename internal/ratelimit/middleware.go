package ratelimit

import (
	"net/http"
	"strconv"
	"time"
)

// Config describes how to derive a rate limit key and thresholds.
type Config struct {
	Key    func(*http.Request) string
	Window time.Duration
	Max    int
}

// Handler enforces rate limits before delegating to the next handler.
type Handler struct {
	Limiter Limiter
	Config  Config
	OnError func(error)
}

// Middleware implements the http.Handler middleware interface.
func (h Handler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.Config.Key == nil {
			next.ServeHTTP(w, r)
			return
		}
		key := h.Config.Key(r)
		allowed, remaining, resetAt, err := h.Limiter.Allow(r.Context(), key, h.Config.Window, h.Config.Max)
		if err != nil {
			if h.OnError != nil {
				h.OnError(err)
			}
			next.ServeHTTP(w, r)
			return
		}

		limitValue := h.Config.Max
		if limitValue < 0 {
			limitValue = 0
		}
		headers := w.Header()
		headers.Set("X-RateLimit-Limit", strconv.Itoa(limitValue))
		headers.Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		headers.Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

		if !allowed {
			retryAfter := int(time.Until(resetAt).Seconds())
			if retryAfter < 0 {
				retryAfter = 0
			}
			headers.Set("Retry-After", strconv.Itoa(retryAfter))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
