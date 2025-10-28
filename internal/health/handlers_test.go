package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noah-isme/backend-toko/internal/health"
)

type stubChecker struct {
	dbErr    error
	redisErr error
}

func (s stubChecker) PingDB(_ context.Context, _ time.Duration) error {
	return s.dbErr
}

func (s stubChecker) PingRedis(_ context.Context, _ time.Duration) error {
	return s.redisErr
}

func TestLive(t *testing.T) {
	handler := health.Handler{}
	rr := httptest.NewRecorder()
	handler.Live(rr, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	if body := rr.Body.String(); body != "ok" {
		t.Fatalf("unexpected body %q", body)
	}
}

func TestReadySuccess(t *testing.T) {
	handler := health.Handler{Checker: stubChecker{}, DBTimeout: 50 * time.Millisecond, RedisTimeout: 50 * time.Millisecond}
	rr := httptest.NewRecorder()
	handler.Ready(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 got %d", rr.Code)
	}
	var status map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status["db"] != "ok" || status["redis"] != "ok" {
		t.Fatalf("unexpected status %#v", status)
	}
}

func TestReadyFailure(t *testing.T) {
	handler := health.Handler{Checker: stubChecker{dbErr: errors.New("db down")}, DBTimeout: 10 * time.Millisecond, RedisTimeout: 10 * time.Millisecond}
	rr := httptest.NewRecorder()
	handler.Ready(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 got %d", rr.Code)
	}
}
