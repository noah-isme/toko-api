package security

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/noah-isme/backend-toko/internal/common"
)

// BodyLimit enforces a maximum request payload size.
//
// Overrides replace Max for specific routes (exact method and path match on
// r.URL.Path, before routing). An overridden route is not buffered: its body
// is wrapped in http.MaxBytesReader so the handler streams it and can apply
// its own, possibly smaller, cap and error message.
type BodyLimit struct {
	Max       int64
	Overrides []BodyLimitOverride
}

// BodyLimitOverride sets a different payload cap for one route.
type BodyLimitOverride struct {
	Method string
	Path   string
	Max    int64
}

// Middleware rejects requests exceeding the configured limit with HTTP 413 and
// the canonical JSON error envelope (code PAYLOAD_TOO_LARGE).
func (b BodyLimit) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}

		if o, ok := b.override(r); ok {
			if o.Max > 0 {
				if r.ContentLength > o.Max {
					writeTooLarge(w, o.Max)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, o.Max)
			}
			next.ServeHTTP(w, r)
			return
		}

		if b.Max <= 0 {
			next.ServeHTTP(w, r)
			return
		}

		if r.ContentLength > b.Max {
			writeTooLarge(w, b.Max)
			return
		}

		limited := io.LimitReader(r.Body, b.Max+1)
		buf, err := io.ReadAll(limited)
		if err != nil && !errors.Is(err, io.EOF) {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request body", nil)
			return
		}
		if int64(len(buf)) > b.Max {
			writeTooLarge(w, b.Max)
			return
		}

		_ = r.Body.Close()

		r.Body = io.NopCloser(bytes.NewReader(buf))
		r.ContentLength = int64(len(buf))
		next.ServeHTTP(w, r)
	})
}

func (b BodyLimit) override(r *http.Request) (BodyLimitOverride, bool) {
	for _, o := range b.Overrides {
		if o.Method == r.Method && o.Path == r.URL.Path {
			return o, true
		}
	}
	return BodyLimitOverride{}, false
}

func writeTooLarge(w http.ResponseWriter, max int64) {
	common.JSONError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
		fmt.Sprintf("request body exceeds %d bytes", max), nil)
}
