package alerts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFlusher struct {
	mu      sync.Mutex
	calls   int
	results []Result // consumed in order; zero Result afterwards
	err     error
	block   chan struct{} // when set, Flush waits on it
	entered chan struct{} // signalled on each Flush entry
	ctxs    []context.Context
}

func (f *fakeFlusher) Flush(ctx context.Context) (Result, error) {
	f.mu.Lock()
	f.calls++
	f.ctxs = append(f.ctxs, ctx)
	var res Result
	if len(f.results) > 0 {
		res = f.results[0]
		f.results = f.results[1:]
	}
	block, entered := f.block, f.entered
	f.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if block != nil {
		<-block
	}
	return res, f.err
}

func (f *fakeFlusher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestKickNilRunner(t *testing.T) {
	var r *Runner
	r.Kick() // must not panic
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	var k Kicker = r
	k.Kick()
}

func TestKickNeverBlocks(t *testing.T) {
	r := &Runner{Flusher: &fakeFlusher{}}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			r.Kick() // runner not started: channel fills after one
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Kick blocked")
	}
}

func TestRunnerLoopsWhileMore(t *testing.T) {
	f := &fakeFlusher{results: []Result{{Claimed: 2, More: true}, {Events: 1}}}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: time.Second}
	r.Start(context.Background())
	defer func() { _ = r.Stop(context.Background()) }()
	r.Kick()
	waitFor(t, time.Second, func() bool { return f.count() >= 2 })
	time.Sleep(20 * time.Millisecond)
	if got := f.count(); got != 2 {
		t.Fatalf("Flush calls = %d, want 2", got)
	}
}

func TestRunnerStopsWithoutProgress(t *testing.T) {
	// More=true but nothing moved (claim budget exhausted): must not hot-loop.
	f := &fakeFlusher{results: []Result{{More: true}, {More: true}, {More: true}}}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: time.Second}
	r.Start(context.Background())
	defer func() { _ = r.Stop(context.Background()) }()
	r.Kick()
	waitFor(t, time.Second, func() bool { return f.count() >= 1 })
	time.Sleep(20 * time.Millisecond)
	if got := f.count(); got != 1 {
		t.Fatalf("Flush calls = %d, want 1", got)
	}
}

func TestRunnerStopsOnError(t *testing.T) {
	var errCount atomic.Int32
	f := &fakeFlusher{results: []Result{{Claimed: 1, More: true}}, err: errors.New("db down")}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: time.Second, OnError: func(error) { errCount.Add(1) }}
	r.Start(context.Background())
	defer func() { _ = r.Stop(context.Background()) }()
	r.Kick()
	waitFor(t, time.Second, func() bool { return errCount.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	if f.count() != 1 {
		t.Fatalf("Flush calls = %d, want 1", f.count())
	}
}

func TestRunnerKicksWhileInFlightCoalesce(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 4)
	f := &fakeFlusher{block: block, entered: entered}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: 5 * time.Second}
	r.Start(context.Background())
	defer func() { _ = r.Stop(context.Background()) }()

	r.Kick()
	<-entered
	r.Kick()
	r.Kick()
	r.Kick()
	close(block) // let all runs finish immediately from now on
	waitFor(t, time.Second, func() bool { return f.count() >= 2 })
	time.Sleep(30 * time.Millisecond)
	if got := f.count(); got != 2 {
		t.Fatalf("Flush calls = %d, want 2 (one in flight + one coalesced kick)", got)
	}
}

func TestRunnerTicker(t *testing.T) {
	f := &fakeFlusher{}
	r := &Runner{Flusher: f, Interval: 10 * time.Millisecond, Timeout: time.Second}
	r.Start(context.Background())
	defer func() { _ = r.Stop(context.Background()) }()
	waitFor(t, time.Second, func() bool { return f.count() >= 2 })
}

func TestRunnerStopWaitsForInFlight(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	f := &fakeFlusher{block: block, entered: entered}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: 5 * time.Second}
	r.Start(context.Background())
	r.Kick()
	<-entered

	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(context.Background()) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a Flush was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	// The in-flight Flush context must not be cancelled by Stop.
	f.mu.Lock()
	fctx := f.ctxs[0]
	f.mu.Unlock()
	if fctx.Err() != nil {
		t.Fatal("Stop cancelled the in-flight Flush context")
	}
	close(block)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the in-flight run completed")
	}
}

func TestRunnerStopBoundedByContext(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	entered := make(chan struct{}, 1)
	f := &fakeFlusher{block: block, entered: entered}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: 5 * time.Second}
	r.Start(context.Background())
	r.Kick()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop err = %v, want deadline exceeded", err)
	}
}

func TestRunnerStopPreventsFurtherFlushes(t *testing.T) {
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	f := &fakeFlusher{block: block, entered: entered, results: []Result{{Claimed: 1, More: true}, {Claimed: 1, More: true}}}
	r := &Runner{Flusher: f, Interval: time.Hour, Timeout: 5 * time.Second}
	r.Start(context.Background())
	r.Kick()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	close(block)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if got := f.count(); got != 1 {
		t.Fatalf("Flush calls = %d, want 1 after Stop", got)
	}
}
