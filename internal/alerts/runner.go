package alerts

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultInterval = 15 * time.Second
	defaultTimeout  = 60 * time.Second
)

// Kicker asks the background runner to drain the outbox soon. Handlers that
// change stock or prices call Kick after their writes; it never blocks.
type Kicker interface {
	Kick()
}

// Flusher is the unit of work the Runner repeats. *Dispatcher satisfies it.
type Flusher interface {
	Flush(ctx context.Context) (Result, error)
}

// Runner drains the alert outbox in the background: on a ticker and on Kick.
// One run is in flight at a time per process; a run loops Flush while more
// work remains and progress is being made, bounded by Timeout. Multiple API
// replicas may each run one; the Dispatcher's row locks keep them from
// double-notifying.
type Runner struct {
	Flusher  Flusher
	Interval time.Duration
	Timeout  time.Duration
	OnError  func(error)
	Log      func(Result)

	initOnce sync.Once
	kick     chan struct{}
	running  atomic.Bool
	wg       sync.WaitGroup

	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}
}

func (r *Runner) init() {
	r.initOnce.Do(func() {
		r.kick = make(chan struct{}, 1)
	})
}

// Kick requests a run without blocking. It is a no-op on a nil Runner.
func (r *Runner) Kick() {
	if r == nil {
		return
	}
	r.init()
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// Start launches the background loop. It returns immediately; the loop ends
// when ctx is cancelled or Stop is called.
func (r *Runner) Start(ctx context.Context) {
	if r == nil || r.Flusher == nil {
		return
	}
	r.init()
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.stopped = make(chan struct{})
	r.mu.Unlock()

	interval := r.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				r.runOnce(loopCtx)
			case <-r.kick:
				r.runOnce(loopCtx)
			}
		}
	}()
	go func() {
		r.wg.Wait()
		close(r.stopped)
	}()
}

// Stop ends the loop and waits for an in-flight run (including the
// post-commit deliveries of its current Flush) until ctx expires. A run stops
// starting new Flushes as soon as Stop is called. It returns ctx.Err() when
// the wait was cut short.
func (r *Runner) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel, stopped := r.cancel, r.stopped
	r.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runOnce performs one drain run unless another is already in flight. The
// Flush context is detached from loopCtx so shutdown never aborts a
// transaction midway; loopCtx only stops further iterations.
func (r *Runner) runOnce(loopCtx context.Context) {
	if !r.running.CompareAndSwap(false, true) {
		return
	}
	defer r.running.Store(false)
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	fctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		res, err := r.Flusher.Flush(fctx)
		if err != nil {
			if r.OnError != nil {
				r.OnError(err)
			}
			return
		}
		if r.Log != nil && (res.Progress() || res.More) {
			r.Log(res)
		}
		// Stop when drained, when nothing moved (e.g. the claim budget for
		// the remaining time is zero), or when shutting down.
		if !res.More || !res.Progress() || loopCtx.Err() != nil || fctx.Err() != nil {
			return
		}
	}
}
