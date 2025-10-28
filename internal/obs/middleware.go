package obs

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// StatusRecorder wraps ResponseWriter to capture status code and bytes written.
type StatusRecorder struct {
	http.ResponseWriter
	status       int
	bytesWritten int64
}

// NewStatusRecorder constructs a status recorder with default 200 status.
func NewStatusRecorder(w http.ResponseWriter) *StatusRecorder {
	return &StatusRecorder{ResponseWriter: w, status: http.StatusOK}
}

// WriteHeader stores the status code before delegating.
func (sr *StatusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

// Write records the number of bytes written.
func (sr *StatusRecorder) Write(p []byte) (int, error) {
	n, err := sr.ResponseWriter.Write(p)
	sr.bytesWritten += int64(n)
	return n, err
}

// Status returns the response status code.
func (sr *StatusRecorder) Status() int { return sr.status }

// BytesWritten returns the number of bytes written to the client.
func (sr *StatusRecorder) BytesWritten() int64 { return sr.bytesWritten }

// HTTPObs instruments HTTP handlers with metrics.
type HTTPObs struct {
	Metrics *HTTPMetrics
}

// Middleware instruments request/response lifecycle with counters and histograms.
func (o HTTPObs) Middleware(next http.Handler) http.Handler {
	if o.Metrics == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := NewStatusRecorder(w)
		o.Metrics.InFlight.Inc()
		start := time.Now()
		next.ServeHTTP(recorder, r)
		o.Metrics.InFlight.Dec()

		route := RoutePatternFromContext(r.Context())
		if route == "" {
			if rc := chi.RouteContext(r.Context()); rc != nil {
				route = rc.RoutePattern()
			}
		}
		if route == "" {
			route = "unknown"
		}
		status := strconv.Itoa(recorder.Status())
		o.Metrics.ReqTotal.WithLabelValues(r.Method, route, status).Inc()
		o.Metrics.ReqDur.WithLabelValues(r.Method, route).Observe(DurationMillis(time.Since(start)))
	})
}

// RoutePatternMiddleware injects the matched route pattern into request context.
func RoutePatternMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if rc := chi.RouteContext(ctx); rc != nil {
			if pattern := rc.RoutePattern(); pattern != "" {
				ctx = WithRoutePattern(ctx, pattern)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TracingMiddleware starts an OpenTelemetry span for each incoming request.
func TracingMiddleware(next http.Handler) http.Handler {
	tracer := otel.Tracer("http.server")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := RoutePatternFromContext(r.Context())
		if route == "" {
			if rc := chi.RouteContext(r.Context()); rc != nil {
				route = rc.RoutePattern()
			}
		}
		if route == "" {
			route = r.URL.Path
		}
		ctx, span := tracer.Start(r.Context(), fmt.Sprintf("%s %s", r.Method, route))
		recorder := NewStatusRecorder(w)
		next.ServeHTTP(recorder, r.WithContext(ctx))

		span.SetAttributes(
			attribute.String("http.method", r.Method),
			attribute.String("http.route", route),
			attribute.String("http.target", r.URL.Path),
			attribute.Int("http.status_code", recorder.Status()),
		)
		if recorder.Status() >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(recorder.Status()))
		}
		span.End()
	})
}
