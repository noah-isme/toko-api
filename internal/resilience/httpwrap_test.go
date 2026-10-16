package resilience

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

func TestHTTPClientLogsRedactURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // force a transport error (*url.Error) on every attempt

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	var buf bytes.Buffer
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	}))
	ctx = zerolog.New(&buf).With().Str("trace_id", "ctx-trace").Logger().WithContext(ctx)

	client := HTTPClient{
		Client:      &http.Client{Timeout: time.Second},
		Breaker:     NewBreaker(100, 1, time.Second),
		BaseBackoff: time.Millisecond,
		MaxAttempts: 2,
		Target:      "webhook-test",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/hook/secret-path?token=secret", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, doErr := client.Do(ctx, req)
	if doErr == nil {
		t.Fatalf("expected transport error")
	}
	var ue *url.Error
	if !errors.As(doErr, &ue) {
		t.Fatalf("expected *url.Error, got %T: %v", doErr, doErr)
	}
	if !strings.Contains(doErr.Error(), "secret-path") {
		t.Fatalf("returned error must keep the original URL, got %q", doErr.Error())
	}

	out := buf.String()
	if out == "" {
		t.Fatalf("expected log output")
	}
	if strings.Contains(out, "secret") {
		t.Fatalf("logs leak secret: %s", out)
	}
	if !strings.Contains(out, parsed.Host) {
		t.Fatalf("logs missing host %q: %s", parsed.Host, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if n := strings.Count(line, `"trace_id"`); n != 1 {
			t.Fatalf("expected exactly one trace_id key, got %d in %s", n, line)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
	}
}

func TestRedactURLErrorDoesNotMutateOriginal(t *testing.T) {
	inner := errors.New("connection refused")
	orig := &url.Error{Op: "Post", URL: "https://user:pw@hooks.example.com/hook/secret?token=abc", Err: inner}
	before := orig.Error()

	red := redactURLError(orig)

	if orig.Error() != before {
		t.Fatalf("original error mutated: %q -> %q", before, orig.Error())
	}
	rue, ok := red.(*url.Error)
	if !ok {
		t.Fatalf("expected *url.Error, got %T", red)
	}
	if rue == orig {
		t.Fatalf("expected a copy, got the same pointer")
	}
	if rue.URL != "https://hooks.example.com" || rue.Op != "Post" || !errors.Is(rue, inner) {
		t.Fatalf("unexpected redacted error: %+v", rue)
	}

	plain := errors.New("500 Internal Server Error")
	if redactURLError(plain) != plain {
		t.Fatalf("non-URL errors must be returned unchanged")
	}
	if redactURLError(nil) != nil {
		t.Fatalf("nil must stay nil")
	}
}
