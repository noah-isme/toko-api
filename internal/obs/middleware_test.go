package obs_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/noah-isme/backend-toko/internal/obs"
)

func TestHTTPMetricsLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := obs.NewHTTPMetrics("toko", []float64{1, 10}, registry)
	handler := obs.HTTPObs{Metrics: metrics}.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	req = req.WithContext(obs.WithRoutePattern(req.Context(), "/health/ready"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 got %d", rr.Code)
	}

	total := testutil.ToFloat64(metrics.ReqTotal.WithLabelValues(http.MethodGet, "/health/ready", "204"))
	if total != 1 {
		t.Fatalf("expected counter to be 1, got %v", total)
	}

	samples := testutil.CollectAndCount(metrics.ReqDur)
	if samples == 0 {
		t.Fatalf("expected histogram sample")
	}

	if metrics.InFlight != nil {
		if val := testutil.ToFloat64(metrics.InFlight); val != 0 {
			t.Fatalf("expected no in-flight requests, got %v", val)
		}
	}
}
