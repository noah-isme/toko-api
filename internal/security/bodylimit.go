package security

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

// BodyLimit enforces a maximum request payload size.
type BodyLimit struct {
	Max int64
}

// Middleware rejects requests exceeding the configured limit with HTTP 413.
func (b BodyLimit) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b.Max <= 0 || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}

		if r.ContentLength > b.Max && r.ContentLength != -1 {
			http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
			return
		}

		limited := io.LimitReader(r.Body, b.Max+1)
		buf, err := io.ReadAll(limited)
		if err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if int64(len(buf)) > b.Max {
			http.Error(w, "request entity too large", http.StatusRequestEntityTooLarge)
			return
		}

		_ = r.Body.Close()

		r.Body = io.NopCloser(bytes.NewReader(buf))
		r.ContentLength = int64(len(buf))
		next.ServeHTTP(w, r)
	})
}
