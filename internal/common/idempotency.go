package common

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Idem provides an Idempotency-Key middleware backed by Redis.
type Idem struct {
	R   *redis.Client
	TTL time.Duration
}

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "idem:" + hex.EncodeToString(sum[:])
}

// Middleware enforces idempotency semantics for write endpoints.
func (i Idem) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Idempotency-Key")
		if header == "" || i.R == nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		key := hashKey(header)
		ok, err := i.R.SetNX(ctx, key, "locked", i.TTL).Result()
		if err != nil {
			commonJSONError(w, err)
			return
		}
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, "{\"error\":{\"code\":\"IDEMPOTENT_REPLAY\",\"message\":\"duplicate request\"}}")
			return
		}
		rec := &idemStatusRecorder{ResponseWriter: w}
		defer func() {
			// A 5xx means the request failed (or must be retried to complete, as with
			// a refund awaiting reconciliation): release the key so the client can
			// retry with the same Idempotency-Key. Otherwise keep it for the TTL; this
			// also runs when the handler panics (status unset), so the key still expires.
			if rec.status >= http.StatusInternalServerError {
				_ = i.R.Del(context.Background(), key).Err()
				return
			}
			_ = i.R.Expire(context.Background(), key, i.TTL).Err()
		}()
		next.ServeHTTP(rec, r)
	})
}

// idemStatusRecorder records the response status for the idempotency middleware.
type idemStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *idemStatusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *idemStatusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer (Flush, deadlines).
func (r *idemStatusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func commonJSONError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	JSONError(w, http.StatusInternalServerError, "INTERNAL", "idempotency store error", map[string]any{"error": err.Error()})
}
