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
		defer func() {
			// ensure the key expires even if handler panics
			_ = i.R.Expire(context.Background(), key, i.TTL).Err()
		}()
		next.ServeHTTP(w, r)
	})
}

func commonJSONError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	JSONError(w, http.StatusInternalServerError, "INTERNAL", "idempotency store error", map[string]any{"error": err.Error()})
}
