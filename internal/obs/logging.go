package obs

import (
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"

	"github.com/noah-isme/backend-toko/internal/common"
)

// NewLogger configures a zerolog logger using the provided format and level.
func NewLogger(format, level string) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339
	lvl, err := zerolog.ParseLevel(strings.ToLower(strings.TrimSpace(level)))
	if err != nil {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	writer := os.Stdout
	var out io.Writer = writer
	if strings.ToLower(strings.TrimSpace(format)) == "console" || strings.ToLower(strings.TrimSpace(format)) == "text" {
		out = zerolog.ConsoleWriter{Out: writer, TimeFormat: time.RFC3339}
	}
	logger := zerolog.New(out).With().Timestamp().Logger()
	return logger
}

// RequestLogger records structured HTTP request logs enriched with tracing metadata.
type RequestLogger struct {
	Logger zerolog.Logger
}

// Middleware implements chi middleware for structured request logs.
func (l RequestLogger) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := NewStatusRecorder(w)
		start := time.Now()
		next.ServeHTTP(recorder, r)

		duration := time.Since(start)
		route := RoutePatternFromContext(r.Context())
		if route == "" {
			route = r.URL.Path
		}
		reqID := middleware.GetReqID(r.Context())
		spanCtx := trace.SpanContextFromContext(r.Context())
		traceID := ""
		spanID := ""
		if spanCtx.IsValid() {
			traceID = spanCtx.TraceID().String()
			spanID = spanCtx.SpanID().String()
		}
		userID, _ := common.UserID(r.Context())

		evt := l.Logger.Info().
			Str("method", r.Method).
			Str("route", route).
			Str("path", r.URL.Path).
			Int("status", recorder.Status()).
			Int64("duration_ms", duration.Milliseconds()).
			Int64("bytes", recorder.BytesWritten()).
			Str("request_id", reqID).
			Str("trace_id", traceID).
			Str("span_id", spanID)
		if user := strings.TrimSpace(userID); user != "" {
			evt = evt.Str("user_id", user)
		}
		if host := strings.TrimSpace(r.Host); host != "" {
			evt = evt.Str("host", host)
		}
		if ip := strings.TrimSpace(r.RemoteAddr); ip != "" {
			evt = evt.Str("remote_addr", ip)
		}
		if ua := strings.TrimSpace(r.UserAgent()); ua != "" {
			evt = evt.Str("user_agent", ua)
		}
		evt.Msg("http_request")
	})
}
