package ratelimit

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestLimiterAllowSlidingWindow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("run miniredis: %v", err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()
	limiter := Limiter{Client: client, Prefix: "test:"}

	ctx := context.Background()
	window := 2 * time.Second
	max := 2

	for i := 0; i < max; i++ {
		allowed, remaining, _, err := limiter.Allow(ctx, "key", window, max)
		if err != nil {
			t.Fatalf("allow: %v", err)
		}
		if !allowed {
			t.Fatalf("expected request %d to be allowed", i)
		}
		if remaining != max-(i+1) {
			t.Fatalf("unexpected remaining: %d", remaining)
		}
	}

	allowed, remaining, _, err := limiter.Allow(ctx, "key", window, max)
	if err != nil {
		t.Fatalf("allow: %v", err)
	}
	if allowed {
		t.Fatal("expected third request to be rejected")
	}
	if remaining != 0 {
		t.Fatalf("expected remaining 0, got %d", remaining)
	}

	mr.FastForward(window)

	allowed, _, _, err = limiter.Allow(ctx, "key", window, max)
	if err != nil {
		t.Fatalf("allow after window: %v", err)
	}
	if !allowed {
		t.Fatal("expected request after window to be allowed")
	}
}
