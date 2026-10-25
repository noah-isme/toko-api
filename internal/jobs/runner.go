// Package jobs runs the worker's periodic background jobs: checkout expiry
// (expire unpaid orders, sweep orphaned reservations) and shipment tracking.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Result holds a job run's counters, keyed by action (e.g. "orders_expired").
type Result map[string]int

// Add increments a counter.
func (r Result) Add(action string, n int) {
	if n != 0 {
		r[action] += n
	}
}

// Job is one periodic unit of work. Run must be idempotent: the lock only
// makes overlapping runs unlikely, not impossible (see Runner.LockTTL).
type Job interface {
	Name() string
	Run(ctx context.Context) (Result, error)
}

// Spec schedules a job.
type Spec struct {
	Job      Job
	Interval time.Duration
}

// TryLocker takes a lock without waiting; lock.Locker satisfies it.
type TryLocker interface {
	TryLock(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) error) (bool, error)
}

// Runner runs each job on its own ticker. Every tick tries the Redis lock
// "lock:job:<name>" once, so across all worker replicas only one instance runs
// a given job per tick and the others record a skip.
type Runner struct {
	Locker TryLocker
	// LockTTL bounds how long a crashed holder blocks the job. A run that
	// takes longer than this can overlap with the next one, which is safe
	// because every write is conditional.
	LockTTL   time.Duration
	Heartbeat time.Duration
	Logger    *zerolog.Logger
	Jobs      []Spec
	Now       func() time.Time

	mu    sync.Mutex
	state map[string]*jobState
}

type jobState struct {
	lastRun    time.Time
	lastStatus string
}

const (
	statusSuccess = "success"
	statusError   = "error"
	statusSkipped = "skipped"
)

// Run blocks until ctx is done. Each job runs once immediately and then every
// Interval. It returns nil once all job goroutines have stopped.
func (r *Runner) Run(ctx context.Context) error {
	if r.Locker == nil {
		return errors.New("jobs: locker not configured")
	}
	if len(r.Jobs) == 0 {
		return errors.New("jobs: no jobs configured")
	}
	for _, spec := range r.Jobs {
		if spec.Job == nil || spec.Interval <= 0 {
			return errors.New("jobs: every job needs a Job and a positive Interval")
		}
	}
	r.mu.Lock()
	r.state = make(map[string]*jobState, len(r.Jobs))
	for _, spec := range r.Jobs {
		r.state[spec.Job.Name()] = &jobState{lastStatus: "never"}
	}
	r.mu.Unlock()

	logger := r.logger()
	names := make([]string, 0, len(r.Jobs))
	for _, spec := range r.Jobs {
		names = append(names, spec.Job.Name())
	}
	logger.Info().Strs("jobs", names).Msg("periodic jobs starting")

	var wg sync.WaitGroup
	for _, spec := range r.Jobs {
		wg.Add(1)
		go func(spec Spec) {
			defer wg.Done()
			r.loop(ctx, spec)
		}(spec)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.heartbeat(ctx)
	}()
	wg.Wait()
	logger.Info().Msg("periodic jobs stopped")
	return nil
}

func (r *Runner) loop(ctx context.Context, spec Spec) {
	ticker := time.NewTicker(spec.Interval)
	defer ticker.Stop()
	for {
		r.RunOnce(ctx, spec.Job)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce runs one tick of a job under its lock and records the outcome. It
// returns the status (success, error or skipped).
func (r *Runner) RunOnce(ctx context.Context, job Job) string {
	if ctx.Err() != nil {
		return statusSkipped
	}
	name := job.Name()
	logger := r.logger().With().Str("job", name).Logger()
	start := r.now()
	var result Result
	acquired, err := r.Locker.TryLock(ctx, "lock:job:"+name, r.lockTTL(), func(ctx context.Context) error {
		var runErr error
		result, runErr = safeRun(ctx, job)
		return runErr
	})
	elapsed := r.now().Sub(start)

	status := statusSuccess
	switch {
	case !acquired && err == nil:
		status = statusSkipped
		logger.Debug().Str("reason", "lock_held").Msg("job run skipped")
	case err != nil:
		status = statusError
	}
	jobRunsTotal.WithLabelValues(name, status).Inc()
	if acquired {
		jobDuration.WithLabelValues(name).Observe(elapsed.Seconds())
		for action, n := range result {
			if n > 0 {
				jobItemsTotal.WithLabelValues(name, action).Add(float64(n))
			}
		}
	}
	if status == statusSuccess {
		jobLastSuccess.WithLabelValues(name).Set(float64(r.now().Unix()))
	}
	if status != statusSkipped {
		ev := logger.Info()
		if status == statusError {
			ev = logger.Error().Err(err)
		}
		dict := zerolog.Dict()
		for _, action := range sortedKeys(result) {
			dict = dict.Int(action, result[action])
		}
		ev.Str("status", status).Int64("duration_ms", elapsed.Milliseconds()).Dict("counters", dict).Msg("job run")
	}

	r.mu.Lock()
	if st, ok := r.state[name]; ok {
		st.lastRun = r.now()
		st.lastStatus = status
	}
	r.mu.Unlock()
	return status
}

func (r *Runner) heartbeat(ctx context.Context) {
	interval := r.Heartbeat
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		now := r.now()
		dict := zerolog.Dict()
		r.mu.Lock()
		for _, name := range sortedStateKeys(r.state) {
			st := r.state[name]
			age := int64(-1)
			if !st.lastRun.IsZero() {
				age = int64(now.Sub(st.lastRun).Seconds())
			}
			dict = dict.Dict(name, zerolog.Dict().Int64("last_run_age_sec", age).Str("last_status", st.lastStatus))
		}
		r.mu.Unlock()
		r.logger().Info().Dict("jobs", dict).Msg("jobs heartbeat")
	}
}

// safeRun turns a panic inside a job into an error so one faulty run cannot
// take down the worker process (which also delivers webhooks).
func safeRun(ctx context.Context, job Job) (res Result, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job %s panicked: %v\n%s", job.Name(), p, debug.Stack())
		}
	}()
	return job.Run(ctx)
}

func (r *Runner) lockTTL() time.Duration {
	if r.LockTTL <= 0 {
		return 5 * time.Minute
	}
	return r.LockTTL
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) logger() *zerolog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	nop := zerolog.Nop()
	return &nop
}

func sortedKeys(m Result) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStateKeys(m map[string]*jobState) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
