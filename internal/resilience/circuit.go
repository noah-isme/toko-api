package resilience

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"time"
)

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
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Open:
		if time.Since(b.openedAt) >= b.openFor {
			b.state = HalfOpen
			b.failures = 0
			b.successes = 0
			return true
		}
		return false
	default:
		return true
	}
}

// Report records the outcome of a request and transitions the state machine
// when the configured thresholds are exceeded.
func (b *Breaker) Report(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case Open:
		// Ignore reports while open.
		return
	case HalfOpen:
		if success {
			b.state = Closed
			b.reset()
			return
		}
		b.trip()
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
		b.trip()
	} else if total > b.minRequests*2 {
		// prevent unbounded growth of counters
		b.successes = int(math.Ceil(float64(b.successes) * 0.5))
		b.failures = int(math.Ceil(float64(b.failures) * 0.5))
	}
}

func (b *Breaker) trip() {
	b.state = Open
	b.openedAt = time.Now()
	b.failures = 0
	b.successes = 0
}

func (b *Breaker) reset() {
	b.failures = 0
	b.successes = 0
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
