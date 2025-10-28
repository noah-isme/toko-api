package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Checker represents dependencies that can be probed for readiness.
type Checker interface {
	PingDB(ctx context.Context, timeout time.Duration) error
	PingRedis(ctx context.Context, timeout time.Duration) error
}

// Handler exposes HTTP handlers for health endpoints.
type Handler struct {
	Checker      Checker
	DBTimeout    time.Duration
	RedisTimeout time.Duration
}

// Live reports liveness status.
func (h Handler) Live(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Ready reports readiness based on dependency probes.
func (h Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if h.Checker == nil {
		http.Error(w, "dependencies unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	dbStatus := "ok"
	if err := h.Checker.PingDB(ctx, h.dbTimeout()); err != nil {
		dbStatus = err.Error()
	}
	redisStatus := "ok"
	if err := h.Checker.PingRedis(ctx, h.redisTimeout()); err != nil {
		redisStatus = err.Error()
	}
	status := map[string]string{
		"db":    dbStatus,
		"redis": redisStatus,
	}
	if dbStatus != "ok" || redisStatus != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (h Handler) dbTimeout() time.Duration {
	if h.DBTimeout <= 0 {
		return 500 * time.Millisecond
	}
	return h.DBTimeout
}

func (h Handler) redisTimeout() time.Duration {
	if h.RedisTimeout <= 0 {
		return 300 * time.Millisecond
	}
	return h.RedisTimeout
}
