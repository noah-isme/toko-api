package obs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

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

func TestRequestLoggerAttachesContextLogger(t *testing.T) {
	var buf bytes.Buffer
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zerolog.Ctx(r.Context()).Error().Msg("boom")
		w.WriteHeader(http.StatusInternalServerError)
	})
	handler := middleware.RequestID(obs.RequestLogger{Logger: zerolog.New(&buf)}.Middleware(inner))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var boom, access map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid log line %q: %v", line, err)
		}
		switch m["message"] {
		case "boom":
			boom = m
		case "http_request":
			access = m
		}
	}
	if boom == nil {
		t.Fatalf("handler log line missing; buf=%s", buf.String())
	}
	if access == nil {
		t.Fatalf("access log line missing; buf=%s", buf.String())
	}
	if boom["level"] != "error" {
		t.Fatalf("expected level error, got %v", boom["level"])
	}
	reqID, _ := boom["request_id"].(string)
	if reqID == "" {
		t.Fatalf("expected request_id on handler log line, got %v", boom)
	}
	if access["request_id"] != reqID {
		t.Fatalf("request_id mismatch: handler %q access %v", reqID, access["request_id"])
	}
}
