package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestHandlerMiddlewareEnforcesLimit(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("run miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()

	handler := Handler{
		Limiter: Limiter{Client: client, Prefix: "ratelimit:"},
		Config: Config{
			Key:    func(*http.Request) string { return "static" },
			Window: time.Second,
			Max:    1,
		},
	}

	counted := handler.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr1 := httptest.NewRecorder()
	counted.ServeHTTP(rr1, req.Clone(req.Context()))
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected first request allowed, got %d", rr1.Code)
	}

	rr2 := httptest.NewRecorder()
	counted.ServeHTTP(rr2, req.Clone(req.Context()))
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on second request, got %d", rr2.Code)
	}
	if rr2.Header().Get("X-RateLimit-Limit") != "1" {
		t.Fatalf("unexpected limit header: %q", rr2.Header().Get("X-RateLimit-Limit"))
	}
}

func TestHandlerMiddlewareOnError(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	handler := Handler{
		Limiter: Limiter{Client: client, Prefix: "ratelimit:"},
		Config: Config{
			Key:    func(*http.Request) string { return "err" },
			Window: time.Second,
			Max:    1,
		},
	}

	called := false
	handler.OnError = func(error) { called = true }

	counted := handler.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	counted.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected handler to proceed on error, got %d", rr.Code)
	}
	if !called {
		t.Fatal("expected OnError callback to be invoked")
	}
	_ = client.Close()
}
