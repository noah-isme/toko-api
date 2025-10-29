package resilience

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/trace"
)

var breakerNopLogger = zerolog.Nop()

// ErrOpenCircuit is returned when the circuit breaker refuses a request.
var ErrOpenCircuit = errors.New("resilience: circuit breaker open")

// State represents the current breaker state.
type State int

const (
	// Closed accepts all requests and tracks failures.
	Closed State = iota
	// Open rejects requests until the cool-off period expires.
	Open
	// HalfOpen allows a limited number of probes to determine recovery.
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Breaker implements a simple failure-ratio circuit breaker.
type Breaker struct {
	mu           sync.Mutex
	state        State
	failures     int
	successes    int
	minRequests  int
	failureRatio float64
	openedAt     time.Time
	openFor      time.Duration
	target       string
	logger       *zerolog.Logger
}

// NewBreaker constructs a breaker that opens when the rolling failure ratio
// exceeds the configured threshold once the minimum number of requests is
// observed.
func NewBreaker(minRequests int, failureRatio float64, openFor time.Duration) *Breaker {
	if minRequests <= 0 {
		minRequests = 1
	}
	if failureRatio <= 0 {
		failureRatio = 0.5
	}
	if failureRatio > 1 {
		failureRatio = 1
	}
	if openFor <= 0 {
		openFor = 30 * time.Second
	}
	return &Breaker{
		state:        Closed,
		minRequests:  minRequests,
		failureRatio: failureRatio,
		openFor:      openFor,
	}
}

// Allow reports whether a request is permitted in the current state. When the
// breaker is open it only permits a request after the cool-off period and moves
// into half-open to sample the downstream dependency.
func (b *Breaker) Allow(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Open:
		if time.Since(b.openedAt) >= b.openFor {
			b.changeStateLocked(ctx, HalfOpen)
			return true
		}
		return false
	default:
		return true
	}
}

// Report records the outcome of a request and transitions the state machine
// when the configured thresholds are exceeded.
func (b *Breaker) Report(ctx context.Context, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Open:
		// Ignore reports while open.
		return
	case HalfOpen:
		if success {
			b.changeStateLocked(ctx, Closed)
			return
		}
		b.changeStateLocked(ctx, Open)
		return
	}

	if success {
		b.successes++
	} else {
		b.failures++
	}

	total := b.failures + b.successes
	if total < b.minRequests {
		return
	}
	ratio := float64(b.failures) / float64(total)
	if ratio >= b.failureRatio {
		b.changeStateLocked(ctx, Open)
	} else if total > b.minRequests*2 {
		// prevent unbounded growth of counters
		b.successes = int(math.Ceil(float64(b.successes) * 0.5))
		b.failures = int(math.Ceil(float64(b.failures) * 0.5))
	}
}

// Backoff returns an exponential backoff duration for the provided attempt.
// Jitter is expressed as a fraction (e.g. 0.2 == 20%).
func Backoff(base time.Duration, attempt int, jitterPct float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	d := base * time.Duration(1<<uint(attempt-1))
	if jitterPct <= 0 {
		return d
	}
	jitter := float64(d) * jitterPct
	delta := (rand.Float64()*2 - 1) * jitter
	return d + time.Duration(delta)
}

// WithTarget sets the logical dependency identifier used for telemetry labels.
func (b *Breaker) WithTarget(target string) *Breaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.target = strings.TrimSpace(target)
	b.recordStateLocked()
	return b
}

// WithLogger configures the logger used for transition events.
func (b *Breaker) WithLogger(logger zerolog.Logger) *Breaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logger = &logger
	return b
}

func (b *Breaker) changeStateLocked(ctx context.Context, next State) {
	prev := b.state
	if prev == next {
		b.recordStateLocked()
		return
	}
	b.state = next
	if next == Open {
		b.openedAt = time.Now()
	}
	if next == Closed {
		b.openedAt = time.Time{}
	}
	b.failures = 0
	b.successes = 0
	b.recordStateLocked()
	b.recordTransition(ctx, prev, next)
}

func (b *Breaker) recordStateLocked() {
	if BreakerState == nil {
		return
	}
	BreakerState.WithLabelValues(b.targetLabel()).Set(stateGaugeValue(b.state))
}

func (b *Breaker) recordTransition(ctx context.Context, from, to State) {
	label := b.targetLabel()
	if BreakerTransitions != nil {
		BreakerTransitions.WithLabelValues(label, from.String(), to.String()).Inc()
	}
	if to == Open && BreakerOpenedTotal != nil {
		BreakerOpenedTotal.WithLabelValues(label).Inc()
	}
	logger := b.loggerFor(ctx)
	traceID := traceIDFromContext(ctx)
	evt := logger.Info().Str("target", label).Str("from_state", from.String()).Str("to_state", to.String())
	if traceID != "" {
		evt = evt.Str("trace_id", traceID)
	}
	evt.Msg("breaker_transition")
}

func (b *Breaker) targetLabel() string {
	trimmed := strings.TrimSpace(b.target)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func (b *Breaker) loggerFor(ctx context.Context) *zerolog.Logger {
	if ctxLogger := zerolog.Ctx(ctx); ctxLogger != nil {
		logger := ctxLogger.With().Logger()
		return &logger
	}
	if b.logger == nil {
		return &breakerNopLogger
	}
	return b.logger
}

func stateGaugeValue(state State) float64 {
	switch state {
	case Closed:
		return 0
	case Open:
		return 1
	case HalfOpen:
		return 2
	default:
		return -1
	}
}

func traceIDFromContext(ctx context.Context) string {
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		return span.TraceID().String()
	}
	return ""
}
