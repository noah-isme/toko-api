package health_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/health"
)

type noopChecker struct{}

func (noopChecker) PingDB(context.Context, time.Duration) error    { return nil }
func (noopChecker) PingRedis(context.Context, time.Duration) error { return nil }

func TestReadinessAfterShutdown(t *testing.T) {
	handler := health.Handler{Checker: noopChecker{}}

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)

	health.SetReady(true)
	resp := httptest.NewRecorder()
	handler.Ready(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	health.SetReady(false)
	resp2 := httptest.NewRecorder()
	handler.Ready(resp2, req)
	require.Equal(t, http.StatusServiceUnavailable, resp2.Code)

	// reset for other tests
	health.SetReady(true)
}
