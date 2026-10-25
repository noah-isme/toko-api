package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/lock"
)

type funcJob struct {
	name string
	fn   func(ctx context.Context) (Result, error)
}

func (j funcJob) Name() string                            { return j.name }
func (j funcJob) Run(ctx context.Context) (Result, error) { return j.fn(ctx) }

func newLocker(t *testing.T) lock.Locker {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return lock.Locker{R: client}
}

func TestRunnerOnlyOneRunnerPerTick(t *testing.T) {
	locker := newLocker(t)
	var inFlight, maxInFlight, runs atomic.Int32
	job := funcJob{name: "exclusive", fn: func(ctx context.Context) (Result, error) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		runs.Add(1)
		time.Sleep(200 * time.Millisecond)
		inFlight.Add(-1)
		return Result{"done": 1}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := &Runner{Locker: locker, LockTTL: time.Minute, Heartbeat: time.Hour, Jobs: []Spec{{Job: job, Interval: 50 * time.Millisecond}}}
			require.NoError(t, r.Run(ctx))
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), maxInFlight.Load())
	require.GreaterOrEqual(t, runs.Load(), int32(2))
}

func TestRunnerContinuesAfterJobError(t *testing.T) {
	locker := newLocker(t)
	var calls atomic.Int32
	job := funcJob{name: "flaky", fn: func(ctx context.Context) (Result, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("boom")
		}
		return Result{}, nil
	}}
	r := &Runner{Locker: locker, Jobs: []Spec{{Job: job, Interval: time.Hour}}}
	r.state = map[string]*jobState{"flaky": {}}
	require.Equal(t, statusError, r.RunOnce(context.Background(), job))
	require.Equal(t, statusSuccess, r.RunOnce(context.Background(), job))
	require.Equal(t, statusSuccess, r.state["flaky"].lastStatus)
}

func TestRunnerRecoversFromPanic(t *testing.T) {
	locker := newLocker(t)
	job := funcJob{name: "panicky", fn: func(ctx context.Context) (Result, error) {
		panic("kaboom")
	}}
	r := &Runner{Locker: locker, Jobs: []Spec{{Job: job, Interval: time.Hour}}}
	require.Equal(t, statusError, r.RunOnce(context.Background(), job))
}

type heldLocker struct{}

func (heldLocker) TryLock(context.Context, string, time.Duration, func(context.Context) error) (bool, error) {
	return false, nil
}

func TestRunnerSkipsWhenLockHeld(t *testing.T) {
	ran := false
	job := funcJob{name: "skipped", fn: func(ctx context.Context) (Result, error) {
		ran = true
		return nil, nil
	}}
	r := &Runner{Locker: heldLocker{}, Jobs: []Spec{{Job: job, Interval: time.Hour}}}
	require.Equal(t, statusSkipped, r.RunOnce(context.Background(), job))
	require.False(t, ran)
}

func TestRunnerStopsOnContextCancel(t *testing.T) {
	locker := newLocker(t)
	var calls atomic.Int32
	job := funcJob{name: "ticker", fn: func(ctx context.Context) (Result, error) {
		calls.Add(1)
		return nil, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{Locker: locker, Heartbeat: 10 * time.Millisecond, Jobs: []Spec{{Job: job, Interval: 10 * time.Millisecond}}}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop after cancel")
	}
	require.GreaterOrEqual(t, calls.Load(), int32(2))
}
