package obs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTPMetrics groups Prometheus collectors for HTTP observability.
type HTTPMetrics struct {
	ReqTotal *prometheus.CounterVec
	ReqDur   *prometheus.HistogramVec
	InFlight prometheus.Gauge
}

// NewHTTPMetrics registers and returns HTTP metrics collectors.
func NewHTTPMetrics(namespace string, buckets []float64, reg prometheus.Registerer) *HTTPMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	if len(buckets) == 0 {
		buckets = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500}
	} else {
		sort.Float64s(buckets)
	}
	m := &HTTPMetrics{
		ReqTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests handled by the server.",
		}, []string{"method", "route", "status"}),
		ReqDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "http_request_duration_ms",
			Help:      "HTTP request latency distribution in milliseconds.",
			Buckets:   buckets,
		}, []string{"method", "route"}),
		InFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "http_in_flight_requests",
			Help:      "Current number of in-flight HTTP requests.",
		}),
	}
	mustRegister(reg, &m.ReqTotal, &m.ReqDur, &m.InFlight)
	return m
}

// ParseBucketsCSV converts a comma-separated list of bucket boundaries (milliseconds) into floats.
func ParseBucketsCSV(csv string) []float64 {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]float64, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		v, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			continue
		}
		if v <= 0 {
			continue
		}
		out = append(out, v)
	}
	return out
}

// DurationMillis converts a duration to milliseconds for metric observation.
func DurationMillis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func mustRegister(reg prometheus.Registerer, counter **prometheus.CounterVec, histo **prometheus.HistogramVec, gauge *prometheus.Gauge) {
	if counter != nil {
		if err := reg.Register(*counter); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
					*counter = existing
				}
			} else {
				panic(fmt.Errorf("register counter: %w", err))
			}
		}
	}
	if histo != nil {
		if err := reg.Register(*histo); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := are.ExistingCollector.(*prometheus.HistogramVec); ok {
					*histo = existing
				}
			} else {
				panic(fmt.Errorf("register histogram: %w", err))
			}
		}
	}
	if gauge != nil {
		if err := reg.Register(*gauge); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				if existing, ok := are.ExistingCollector.(prometheus.Gauge); ok {
					*gauge = existing
				}
			} else {
				panic(fmt.Errorf("register gauge: %w", err))
			}
		}
	}
}
