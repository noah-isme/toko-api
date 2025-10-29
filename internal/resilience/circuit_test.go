package resilience_test

import (
        "context"
        "testing"
        "time"

	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/resilience"
)

func TestBreakerTransitions(t *testing.T) {
        breaker := resilience.NewBreaker(2, 0.5, 50*time.Millisecond)
        ctx := context.Background()

        require.True(t, breaker.Allow(ctx))
        breaker.Report(ctx, false)
        require.True(t, breaker.Allow(ctx))
        breaker.Report(ctx, false)

        require.False(t, breaker.Allow(ctx), "breaker should open after threshold exceeded")

        time.Sleep(60 * time.Millisecond)
        require.True(t, breaker.Allow(ctx), "breaker should move to half-open after cool off")
        breaker.Report(ctx, true)
        require.True(t, breaker.Allow(ctx), "breaker should close after successful probe")
}

func TestBackoffWithJitter(t *testing.T) {
	base := 100 * time.Millisecond
	d1 := resilience.Backoff(base, 1, 0)
	require.Equal(t, base, d1)

	d2 := resilience.Backoff(base, 3, 0)
	require.Equal(t, base*4, d2)

	// With jitter the delay should stay within expected range.
	d3 := resilience.Backoff(base, 2, 0.2)
	min := base*2 - (base * 2 / 5)
	max := base*2 + (base * 2 / 5)
	require.GreaterOrEqual(t, d3, min)
	require.LessOrEqual(t, d3, max)
}
