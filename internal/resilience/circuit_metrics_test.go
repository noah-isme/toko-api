package resilience_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/resilience"
)

func TestBreakerMetricsTransitions(t *testing.T) {
	resilience.BreakerState.Reset()
	resilience.BreakerTransitions.Reset()
	resilience.BreakerOpenedTotal.Reset()

	breaker := resilience.NewBreaker(1, 0.5, 20*time.Millisecond).WithTarget("webhook")
	ctx := context.Background()

	require.True(t, breaker.Allow(ctx))
	breaker.Report(ctx, false)

	val := testutil.ToFloat64(resilience.BreakerState.WithLabelValues("webhook"))
	require.Equal(t, 1.0, val)

	require.Eventually(t, func() bool {
		return breaker.Allow(ctx)
	}, 100*time.Millisecond, 5*time.Millisecond)

	val = testutil.ToFloat64(resilience.BreakerState.WithLabelValues("webhook"))
	require.Equal(t, 2.0, val)

	breaker.Report(ctx, true)

	val = testutil.ToFloat64(resilience.BreakerState.WithLabelValues("webhook"))
	require.Equal(t, 0.0, val)

	opened := testutil.ToFloat64(resilience.BreakerOpenedTotal.WithLabelValues("webhook"))
	require.Equal(t, 1.0, opened)

	toOpen := testutil.ToFloat64(resilience.BreakerTransitions.WithLabelValues("webhook", "closed", "open"))
	require.Equal(t, 1.0, toOpen)

	toHalf := testutil.ToFloat64(resilience.BreakerTransitions.WithLabelValues("webhook", "open", "half_open"))
	require.Equal(t, 1.0, toHalf)

	toClosed := testutil.ToFloat64(resilience.BreakerTransitions.WithLabelValues("webhook", "half_open", "closed"))
	require.Equal(t, 1.0, toClosed)
}
