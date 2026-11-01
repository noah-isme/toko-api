package alerts

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// fakeMailStore is an in-memory product_alert_deliveries table.
type fakeMailStore struct {
	mu        sync.Mutex
	now       func() time.Time
	rows      map[pgtype.UUID]*fakeDelivery
	order     []pgtype.UUID
	leaseArgs []dbgen.LeaseDueAlertDeliveriesParams
	leaseErr  error
	pruned    int
}

type fakeDelivery struct {
	tenant    pgtype.UUID
	attempts  int32
	next      time.Time
	delivered bool
	failed    bool
	lastErr   string
}

func newFakeMailStore(now func() time.Time, n int) *fakeMailStore {
	s := &fakeMailStore{now: now, rows: map[pgtype.UUID]*fakeDelivery{}}
	for i := 0; i < n; i++ {
		id := newID()
		s.rows[id] = &fakeDelivery{tenant: newID(), next: now()}
		s.order = append(s.order, id)
	}
	return s
}

func (s *fakeMailStore) LeaseDueAlertDeliveries(_ context.Context, arg dbgen.LeaseDueAlertDeliveriesParams) ([]dbgen.LeaseDueAlertDeliveriesRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaseArgs = append(s.leaseArgs, arg)
	if s.leaseErr != nil {
		return nil, s.leaseErr
	}
	var out []dbgen.LeaseDueAlertDeliveriesRow
	for _, id := range s.order {
		r := s.rows[id]
		if r.delivered || r.failed || r.next.After(s.now()) {
			continue
		}
		if len(out) == int(arg.MaxRows) {
			break
		}
		r.attempts++
		r.next = arg.LeaseUntil.Time
		out = append(out, dbgen.LeaseDueAlertDeliveriesRow{
			EventID: id, TenantID: r.tenant, Attempts: r.attempts, Topic: "alert.back_in_stock",
			AggregateID: newID(), Payload: []byte(`{"email":"x@example.com"}`),
		})
	}
	return out, nil
}

func (s *fakeMailStore) MarkAlertDeliveryDelivered(_ context.Context, arg dbgen.MarkAlertDeliveryDeliveredParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[arg.EventID]
	if r.tenant != arg.TenantID {
		return errors.New("tenant mismatch")
	}
	r.delivered = true
	return nil
}

func (s *fakeMailStore) MarkAlertDeliveryFailed(_ context.Context, arg dbgen.MarkAlertDeliveryFailedParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[arg.EventID]
	if r.tenant != arg.TenantID {
		return errors.New("tenant mismatch")
	}
	r.next = arg.NextAttemptAt.Time
	r.lastErr = arg.LastError.String
	r.failed = arg.GiveUp
	return nil
}

func (s *fakeMailStore) PruneFinishedAlertDeliveries(context.Context, dbgen.PruneFinishedAlertDeliveriesParams) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruned++
	return 0, nil
}

func (s *fakeMailStore) counts() (delivered, failed, pending int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		switch {
		case r.delivered:
			delivered++
		case r.failed:
			failed++
		default:
			pending++
		}
	}
	return
}

// slowDeliverer simulates a slow relay that, like the production
// EmailNotifier, ignores its context. It tracks peak concurrency.
type slowDeliverer struct {
	delay    time.Duration
	fail     func(ev dbgen.DomainEvent) error
	inFlight atomic.Int32
	peak     atomic.Int32
	mu       sync.Mutex
	got      []pgtype.UUID
}

func (s *slowDeliverer) Deliver(_ context.Context, ev dbgen.DomainEvent) error {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(s.delay)
	if s.fail != nil {
		if err := s.fail(ev); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.got = append(s.got, ev.ID)
	s.mu.Unlock()
	return nil
}

func TestMailerDeliversBatchWithBoundedConcurrency(t *testing.T) {
	store := newFakeMailStore(time.Now, 7)
	slow := &slowDeliverer{delay: 15 * time.Millisecond}
	m := &Mailer{Q: store, Events: slow, BatchSize: 5, Concurrency: 2, SendTimeout: time.Second}

	res, err := m.Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempted != 5 || res.Delivered != 5 || !res.More || !res.Progress() {
		t.Fatalf("first pass = %+v", res)
	}
	if p := slow.peak.Load(); p > 2 {
		t.Fatalf("peak concurrency %d > 2", p)
	}
	res, err = m.Flush(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempted != 2 || res.Delivered != 2 || res.More {
		t.Fatalf("second pass = %+v", res)
	}
	if d, f, p := store.counts(); d != 7 || f != 0 || p != 0 {
		t.Fatalf("delivered=%d failed=%d pending=%d", d, f, p)
	}
	// Lease covers ceil(5/2)=3 waves + 1 of slack at SendTimeout each.
	lease := store.leaseArgs[0].LeaseUntil.Time.Sub(time.Now())
	if lease < 3500*time.Millisecond || lease > 4*time.Second {
		t.Fatalf("lease = %v", lease)
	}
}

// A run deadline that expires while sends are in flight must not drop any
// leased payload: every leased row ends delivered.
func TestMailerIgnoresRunDeadline(t *testing.T) {
	store := newFakeMailStore(time.Now, 6)
	slow := &slowDeliverer{delay: 20 * time.Millisecond}
	m := &Mailer{Q: store, Events: slow, BatchSize: 10, Concurrency: 1, SendTimeout: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	res, err := m.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("precondition: run deadline should expire during delivery")
	}
	if res.Delivered != 6 || len(slow.got) != 6 {
		t.Fatalf("delivered=%d got=%d", res.Delivered, len(slow.got))
	}
	if d, _, _ := store.counts(); d != 6 {
		t.Fatalf("rows marked delivered = %d, want 6", d)
	}
}

func TestMailerRetriesWithBackoffThenGivesUp(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }

	store := newFakeMailStore(now, 1)
	relayDown := errors.New("dial tcp: connection refused")
	slow := &slowDeliverer{fail: func(dbgen.DomainEvent) error { return relayDown }}
	var errs atomic.Int32
	m := &Mailer{Q: store, Events: slow, MaxAttempts: 3, Now: now, OnError: func(error) { errs.Add(1) }}

	for attempt := 1; attempt <= 3; attempt++ {
		res, err := m.Flush(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if res.Attempted != 1 || res.Delivered != 0 {
			t.Fatalf("attempt %d: %+v", attempt, res)
		}
		// Not due again before its backoff.
		if res, _ := m.Flush(context.Background()); res.Attempted != 0 {
			t.Fatalf("attempt %d: retried before backoff", attempt)
		}
		advance(defaultBackoff(attempt))
	}
	if d, f, p := store.counts(); d != 0 || f != 1 || p != 0 {
		t.Fatalf("delivered=%d failed=%d pending=%d, want gave up", d, f, p)
	}
	for _, r := range store.rows {
		if r.lastErr != relayDown.Error() {
			t.Fatalf("last error = %q", r.lastErr)
		}
	}
	if errs.Load() != 3 {
		t.Fatalf("reported errors = %d, want 3", errs.Load())
	}
}

func TestMailerRecoversAfterTransientFailure(t *testing.T) {
	clock := time.Now()
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	store := newFakeMailStore(now, 1)
	var calls atomic.Int32
	slow := &slowDeliverer{fail: func(dbgen.DomainEvent) error {
		if calls.Add(1) == 1 {
			return errors.New("timeout")
		}
		return nil
	}}
	m := &Mailer{Q: store, Events: slow, Now: now}
	if res, _ := m.Flush(context.Background()); res.Delivered != 0 {
		t.Fatalf("first: %+v", res)
	}
	mu.Lock()
	clock = clock.Add(defaultBackoff(1))
	mu.Unlock()
	if res, _ := m.Flush(context.Background()); res.Delivered != 1 {
		t.Fatalf("second: %+v", res)
	}
}

func TestMailerLeaseErrorAndNilSafety(t *testing.T) {
	var nilMailer *Mailer
	if res, err := nilMailer.Flush(context.Background()); err != nil || res != (Result{}) {
		t.Fatalf("nil: %+v %v", res, err)
	}
	store := newFakeMailStore(time.Now, 1)
	store.leaseErr = errors.New("db down")
	if _, err := (&Mailer{Q: store, Events: &slowDeliverer{}}).Flush(context.Background()); err == nil {
		t.Fatal("lease error must be returned")
	}
}

func TestMailerPrunesHourly(t *testing.T) {
	clock := time.Now()
	var mu sync.Mutex
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	store := newFakeMailStore(now, 0)
	m := &Mailer{Q: store, Events: &slowDeliverer{}, Now: now}
	for i := 0; i < 3; i++ {
		_, _ = m.Flush(context.Background())
	}
	if store.pruned != 1 {
		t.Fatalf("pruned %d times, want 1", store.pruned)
	}
	mu.Lock()
	clock = clock.Add(time.Hour)
	mu.Unlock()
	_, _ = m.Flush(context.Background())
	if store.pruned != 2 {
		t.Fatalf("pruned %d times after an hour, want 2", store.pruned)
	}
	m.Retention = -1
	mu.Lock()
	clock = clock.Add(2 * time.Hour)
	mu.Unlock()
	_, _ = m.Flush(context.Background())
	if store.pruned != 2 {
		t.Fatal("negative retention must disable pruning")
	}
}

func TestDefaultBackoff(t *testing.T) {
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		if got := defaultBackoff(i + 1); got != w {
			t.Errorf("backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}
