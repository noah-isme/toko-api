package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/resilience"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

func newUUID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

// fakeShipQueries backs a real shipping.Service.
type fakeShipQueries struct {
	mu            sync.Mutex
	orders        map[pgtype.UUID]dbgen.Order
	shipments     map[pgtype.UUID]dbgen.Shipment // by order id
	inserted      []dbgen.InsertShipmentEventParams
	orderUpdates  []dbgen.OrderStatus
	mailRecipient string
}

func newFakeShipQueries() *fakeShipQueries {
	return &fakeShipQueries{orders: map[pgtype.UUID]dbgen.Order{}, shipments: map[pgtype.UUID]dbgen.Shipment{}}
}

func (f *fakeShipQueries) GetOrderByID(_ context.Context, id pgtype.UUID) (dbgen.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return dbgen.Order{}, pgx.ErrNoRows
	}
	return o, nil
}

func (f *fakeShipQueries) GetShipmentByOrder(_ context.Context, orderID pgtype.UUID) (dbgen.GetShipmentByOrderRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.shipments[orderID]
	if !ok {
		return dbgen.GetShipmentByOrderRow{}, pgx.ErrNoRows
	}
	return dbgen.GetShipmentByOrderRow{ID: s.ID, OrderID: s.OrderID, Status: s.Status, Courier: s.Courier, TrackingNumber: s.TrackingNumber, LastStatus: s.LastStatus, LastEventAt: s.LastEventAt}, nil
}

// GetOrderByTenant is only used by shipping.Service.Create, which the tracking
// job never calls.
func (f *fakeShipQueries) GetOrderByTenant(context.Context, dbgen.GetOrderByTenantParams) (dbgen.GetOrderByTenantRow, error) {
	return dbgen.GetOrderByTenantRow{}, pgx.ErrNoRows
}

func (f *fakeShipQueries) CreateShipment(context.Context, dbgen.CreateShipmentParams) (dbgen.CreateShipmentRow, error) {
	return dbgen.CreateShipmentRow{}, errors.New("not used")
}

func (f *fakeShipQueries) UpdateOrderStatusIfAllowed(_ context.Context, arg dbgen.UpdateOrderStatusIfAllowedParams) (pgtype.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := f.orders[arg.ID]
	o.Status = arg.Status
	f.orders[arg.ID] = o
	f.orderUpdates = append(f.orderUpdates, arg.Status)
	return arg.ID, nil
}

func (f *fakeShipQueries) InsertShipmentEvent(_ context.Context, arg dbgen.InsertShipmentEventParams) (dbgen.ShipmentEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inserted = append(f.inserted, arg)
	return dbgen.ShipmentEvent{ShipmentID: arg.ShipmentID, Status: arg.Status, OccurredAt: arg.OccurredAt}, nil
}

func (f *fakeShipQueries) UpdateShipmentStatus(_ context.Context, arg dbgen.UpdateShipmentStatusParams) (pgtype.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, s := range f.shipments {
		if s.ID == arg.ID {
			s.Status = arg.Status
			s.LastStatus = dbgen.NullShipmentStatus{ShipmentStatus: arg.Status, Valid: true}
			f.shipments[k] = s
		}
	}
	return arg.ID, nil
}

func (f *fakeShipQueries) GetOrderStatus(_ context.Context, id pgtype.UUID) (dbgen.OrderStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.orders[id].Status, nil
}

func (f *fakeShipQueries) GetUserByID(_ context.Context, id pgtype.UUID) (dbgen.GetUserByIDRow, error) {
	return dbgen.GetUserByIDRow{ID: id, Email: "buyer@example.com"}, nil
}

func (f *fakeShipQueries) insertedStatuses() []dbgen.ShipmentStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]dbgen.ShipmentStatus, 0, len(f.inserted))
	for _, ev := range f.inserted {
		out = append(out, ev.Status)
	}
	return out
}

// fakeTrackingQueries is the job's own query fake.
type fakeTrackingQueries struct {
	mu        sync.Mutex
	tenants   []pgtype.UUID
	due       map[pgtype.UUID][]dbgen.ListShipmentsDueForTrackingRow
	listCalls []dbgen.ListShipmentsDueForTrackingParams
	stopCalls []dbgen.StopTrackingStaleShipmentsParams
	updates   []dbgen.UpdateShipmentTrackingPollParams
}

func (f *fakeTrackingQueries) ListActiveTenantIDs(context.Context) ([]pgtype.UUID, error) {
	return f.tenants, nil
}

func (f *fakeTrackingQueries) ListShipmentsDueForTracking(_ context.Context, arg dbgen.ListShipmentsDueForTrackingParams) ([]dbgen.ListShipmentsDueForTrackingRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, arg)
	rows := f.due[arg.TenantID]
	if int(arg.BatchSize) < len(rows) {
		rows = rows[:arg.BatchSize]
	}
	return rows, nil
}

func (f *fakeTrackingQueries) UpdateShipmentTrackingPoll(_ context.Context, arg dbgen.UpdateShipmentTrackingPollParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, arg)
	return nil
}

func (f *fakeTrackingQueries) StopTrackingStaleShipments(_ context.Context, arg dbgen.StopTrackingStaleShipmentsParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopCalls = append(f.stopCalls, arg)
	return 0, nil
}

type fakeTracker struct {
	mu     sync.Mutex
	result shipping.TrackResult
	err    error
	calls  int
}

func (t *fakeTracker) Track(ctx context.Context, req shipping.TrackReq) ([]shipping.TrackEvent, error) {
	res, err := t.TrackWithSummary(ctx, req)
	return res.Events, err
}

func (t *fakeTracker) TrackWithSummary(context.Context, shipping.TrackReq) (shipping.TrackResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	return t.result, t.err
}

type countingSender struct {
	mu   sync.Mutex
	sent int
}

func (c *countingSender) Send(string, string, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent++
	return nil
}

type recordingEventStore struct {
	mu     sync.Mutex
	topics []string
}

func (s *recordingEventStore) InsertDomainEvent(_ context.Context, arg dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.topics = append(s.topics, arg.Topic)
	return dbgen.DomainEvent{ID: newUUID(), Topic: arg.Topic, AggregateID: arg.AggregateID, Payload: arg.Payload}, nil
}

func (*recordingEventStore) MarkDomainEventDispatched(context.Context, pgtype.UUID) error {
	return nil
}

func (*recordingEventStore) RecordDomainEventDispatchFailure(context.Context, dbgen.RecordDomainEventDispatchFailureParams) error {
	return nil
}

type trackingEnv struct {
	job     *ShipmentTrackingJob
	q       *fakeTrackingQueries
	ship    *fakeShipQueries
	tracker *fakeTracker
	mail    *countingSender
	store   *recordingEventStore
	now     time.Time
	tenant  pgtype.UUID
}

func newTrackingEnv() *trackingEnv {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ship := newFakeShipQueries()
	mail := &countingSender{}
	store := &recordingEventStore{}
	tracker := &fakeTracker{}
	tenantID := newUUID()
	q := &fakeTrackingQueries{tenants: []pgtype.UUID{tenantID}, due: map[pgtype.UUID][]dbgen.ListShipmentsDueForTrackingRow{}}
	svc := &shipping.Service{Q: ship, Mail: mail, NotifyOnShipped: true, NotifyOnOutForDelivery: true, NotifyOnDelivered: true, Events: &events.Bus{Store: store}}
	job := &ShipmentTrackingJob{
		Q: q, Svc: svc, Provider: tracker,
		BatchSize: 100, MaxCallsPerTick: 50, MaxAttempts: 200, MaxAgeDays: 30,
		BackoffBase: 15 * time.Minute, BackoffMax: 6 * time.Hour, SilentAfter: 48 * time.Hour, Jitter: 0.2,
		Now: func() time.Time { return now },
	}
	return &trackingEnv{job: job, q: q, ship: ship, tracker: tracker, mail: mail, store: store, now: now, tenant: tenantID}
}

// addShipment registers a due shipment in the given state for the env tenant.
func (e *trackingEnv) addShipment(tenantID pgtype.UUID, status dbgen.ShipmentStatus, attempts int32, lastEventAt time.Time) dbgen.ListShipmentsDueForTrackingRow {
	orderID, shipmentID := newUUID(), newUUID()
	orderStatus := dbgen.OrderStatusSHIPPED
	if status == dbgen.ShipmentStatusOUTFORDELIVERY {
		orderStatus = dbgen.OrderStatusOUTFORDELIVERY
	}
	e.ship.orders[orderID] = dbgen.Order{ID: orderID, UserID: newUUID(), TenantID: tenantID, Status: orderStatus}
	last := pgtype.Timestamptz{Time: lastEventAt, Valid: true}
	e.ship.shipments[orderID] = dbgen.Shipment{ID: shipmentID, OrderID: orderID, Status: status, LastStatus: dbgen.NullShipmentStatus{ShipmentStatus: status, Valid: true}, LastEventAt: last}
	row := dbgen.ListShipmentsDueForTrackingRow{
		ID: shipmentID, OrderID: orderID, TenantID: tenantID,
		Courier:        pgtype.Text{String: "jne", Valid: true},
		TrackingNumber: pgtype.Text{String: "AWB" + shipmentID.String()[:8], Valid: true},
		Status:         status, LastStatus: dbgen.NullShipmentStatus{ShipmentStatus: status, Valid: true},
		LastEventAt: last, TrackAttempts: attempts,
	}
	e.q.due[tenantID] = append(e.q.due[tenantID], row)
	return row
}

func (e *trackingEnv) run(t *testing.T) Result {
	t.Helper()
	res, err := e.job.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func (e *trackingEnv) lastUpdate(t *testing.T) dbgen.UpdateShipmentTrackingPollParams {
	t.Helper()
	if len(e.q.updates) == 0 {
		t.Fatal("no tracking poll update recorded")
	}
	return e.q.updates[len(e.q.updates)-1]
}

func ev(status string, at time.Time) shipping.TrackEvent {
	return shipping.TrackEvent{Status: status, Description: status + " scan", Location: "KEDIRI", OccurredAt: at.Unix()}
}

func TestShipmentTrackingAppliesForwardEventsInOrder(t *testing.T) {
	e := newTrackingEnv()
	row := e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-2*time.Hour))
	t0 := e.now.Add(-4 * time.Hour)
	e.tracker.result = shipping.TrackResult{Events: []shipping.TrackEvent{
		ev("DELIVERED", t0.Add(3*time.Hour)), ev("OUT_FOR_DELIVERY", t0.Add(2*time.Hour)), ev("SHIPPED", t0.Add(time.Hour)), ev("PICKED", t0),
	}}
	res := e.run(t)
	got := e.ship.insertedStatuses()
	if len(got) != 2 || got[0] != dbgen.ShipmentStatusOUTFORDELIVERY || got[1] != dbgen.ShipmentStatusDELIVERED {
		t.Fatalf("inserted %v, want [OUT_FOR_DELIVERY DELIVERED]", got)
	}
	if st := e.ship.orders[row.OrderID].Status; st != dbgen.OrderStatusDELIVERED {
		t.Fatalf("order status %s, want DELIVERED", st)
	}
	up := e.lastUpdate(t)
	if up.TrackNextPollAt.Valid || up.TrackLastError.String != "stopped: delivered" || up.TenantID != e.tenant {
		t.Fatalf("unexpected poll update %+v", up)
	}
	if res["events_applied"] != 2 || res["stopped_delivered"] != 1 {
		t.Fatalf("counters %v", res)
	}
	if e.mail.sent != 2 || len(e.store.topics) != 2 {
		t.Fatalf("recent events must notify: mail=%d topics=%v", e.mail.sent, e.store.topics)
	}
}

func TestShipmentTrackingAppliesLateUploadedDelivered(t *testing.T) {
	e := newTrackingEnv()
	row := e.addShipment(e.tenant, dbgen.ShipmentStatusOUTFORDELIVERY, 0, e.now.Add(-time.Hour))
	e.tracker.result = shipping.TrackResult{Events: []shipping.TrackEvent{ev("DELIVERED", e.now.Add(-3*time.Hour))}}
	e.run(t)
	if got := e.ship.insertedStatuses(); len(got) != 1 || got[0] != dbgen.ShipmentStatusDELIVERED {
		t.Fatalf("inserted %v", got)
	}
	if st := e.ship.orders[row.OrderID].Status; st != dbgen.OrderStatusDELIVERED {
		t.Fatalf("order status %s", st)
	}
}

func TestShipmentTrackingSilentForOldEvents(t *testing.T) {
	e := newTrackingEnv()
	e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-100*time.Hour))
	e.tracker.result = shipping.TrackResult{Events: []shipping.TrackEvent{ev("OUT_FOR_DELIVERY", e.now.Add(-72*time.Hour))}}
	res := e.run(t)
	if res["events_applied"] != 1 || res["events_applied_silent"] != 1 {
		t.Fatalf("counters %v", res)
	}
	if e.mail.sent != 0 || len(e.store.topics) != 0 {
		t.Fatalf("silent event must not notify: mail=%d topics=%v", e.mail.sent, e.store.topics)
	}
	if len(e.ship.orderUpdates) != 1 || e.ship.orderUpdates[0] != dbgen.OrderStatusOUTFORDELIVERY {
		t.Fatalf("order must still sync, updates=%v", e.ship.orderUpdates)
	}
}

func TestShipmentTrackingBackoffOnNoChange(t *testing.T) {
	e := newTrackingEnv()
	e.job.BackoffMax = 3 * e.job.BackoffBase
	e.tracker.result = shipping.TrackResult{Events: []shipping.TrackEvent{ev("SHIPPED", e.now.Add(-time.Hour))}}
	want := []time.Duration{2 * e.job.BackoffBase, e.job.BackoffMax, e.job.BackoffMax}
	for attempts := int32(0); attempts < 3; attempts++ {
		e.q.due = map[pgtype.UUID][]dbgen.ListShipmentsDueForTrackingRow{}
		e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, attempts, e.now.Add(-time.Hour))
		res := e.run(t)
		up := e.lastUpdate(t)
		if up.TrackAttempts != attempts+1 || !up.TrackNextPollAt.Valid || up.TrackLastError.Valid {
			t.Fatalf("attempt %d: unexpected update %+v", attempts, up)
		}
		if got := up.TrackNextPollAt.Time.Sub(e.now); got != want[attempts] {
			t.Fatalf("attempt %d: next poll +%v, want +%v", attempts, got, want[attempts])
		}
		if res["no_change"] != 1 {
			t.Fatalf("counters %v", res)
		}
	}
	if capDelay(time.Minute, 1, time.Hour) != 2*time.Minute || capDelay(time.Minute, 10, time.Hour) != time.Hour {
		t.Fatal("capDelay math is wrong")
	}
}

func TestShipmentTrackingBackoffOnProviderError(t *testing.T) {
	e := newTrackingEnv()
	e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 2, e.now.Add(-time.Hour))
	e.tracker.err = errors.New("carrier timeout")
	res := e.run(t)
	up := e.lastUpdate(t)
	if up.TrackAttempts != 3 || !up.TrackLastError.Valid || up.TrackLastError.String == "" {
		t.Fatalf("unexpected update %+v", up)
	}
	d := up.TrackNextPollAt.Time.Sub(e.now)
	if d < e.job.BackoffBase || d > e.job.BackoffMax {
		t.Fatalf("next poll +%v outside [base, max]", d)
	}
	if res["errors"] != 1 {
		t.Fatalf("counters %v", res)
	}
}

func TestShipmentTrackingStopsAfterMaxAttempts(t *testing.T) {
	e := newTrackingEnv()
	e.job.MaxAttempts = 5
	e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 4, e.now.Add(-time.Hour))
	e.tracker.err = errors.New("carrier timeout")
	res := e.run(t)
	up := e.lastUpdate(t)
	if up.TrackNextPollAt.Valid || up.TrackLastError.String != "stopped: max_attempts" {
		t.Fatalf("unexpected update %+v", up)
	}
	if res["stopped_max_attempts"] != 1 {
		t.Fatalf("counters %v", res)
	}
}

func TestShipmentTrackingStopsOnReturned(t *testing.T) {
	e := newTrackingEnv()
	e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-time.Hour))
	e.tracker.result = shipping.TrackResult{Terminal: "RETURNED", Events: []shipping.TrackEvent{ev("OUT_FOR_DELIVERY", e.now.Add(-time.Hour))}}
	res := e.run(t)
	if len(e.ship.inserted) != 0 {
		t.Fatalf("no events may be applied, got %v", e.ship.insertedStatuses())
	}
	up := e.lastUpdate(t)
	if up.TrackNextPollAt.Valid || up.TrackLastError.String != "stopped: returned" || res["stopped_terminal"] != 1 {
		t.Fatalf("unexpected update %+v counters %v", up, res)
	}
}

func TestShipmentTrackingRespectsCallCap(t *testing.T) {
	e := newTrackingEnv()
	e.q.tenants = []pgtype.UUID{newUUID(), newUUID(), newUUID()}
	for _, tid := range e.q.tenants {
		for i := 0; i < 10; i++ {
			e.addShipment(tid, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-time.Hour))
		}
	}
	e.job.MaxCallsPerTick = 12
	e.tracker.result = shipping.TrackResult{Events: []shipping.TrackEvent{ev("SHIPPED", e.now.Add(-time.Hour))}}
	res := e.run(t)
	if e.tracker.calls != 12 {
		t.Fatalf("tracker called %d times, want 12", e.tracker.calls)
	}
	if len(e.q.updates) != 12 {
		t.Fatalf("updates %d, want 12 (untouched rows keep their schedule)", len(e.q.updates))
	}
	if res["deferred_cap"] == 0 {
		t.Fatalf("counters %v", res)
	}
	if len(e.q.stopCalls) != 3 {
		t.Fatalf("stale stop must run for every tenant, got %d", len(e.q.stopCalls))
	}
}

func TestShipmentTrackingSkipsWhenBreakerOpen(t *testing.T) {
	e := newTrackingEnv()
	e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-time.Hour))
	e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-time.Hour))
	breaker := resilience.NewBreaker(1, 0.01, time.Hour)
	breaker.Report(context.Background(), false)
	e.job.Provider = BreakerProvider{Inner: e.tracker, Breaker: breaker}
	res := e.run(t)
	if len(e.q.updates) != 0 || e.tracker.calls != 0 {
		t.Fatalf("breaker open: updates=%d calls=%d", len(e.q.updates), e.tracker.calls)
	}
	if res["skipped_breaker"] < 1 {
		t.Fatalf("counters %v", res)
	}
}

func TestShipmentTrackingPassesTenantAndMaxAge(t *testing.T) {
	e := newTrackingEnv()
	other := newUUID()
	e.q.tenants = append(e.q.tenants, other)
	e.job.MaxAgeDays = 9
	e.job.BatchSize = 7
	e.run(t)
	if len(e.q.stopCalls) != 2 || len(e.q.listCalls) != 2 {
		t.Fatalf("stop=%d list=%d, want one per tenant", len(e.q.stopCalls), len(e.q.listCalls))
	}
	for i, tid := range []pgtype.UUID{e.tenant, other} {
		if got := e.q.listCalls[i]; got.TenantID != tid || got.MaxAgeDays != 9 || got.BatchSize != 7 {
			t.Fatalf("list params %+v", got)
		}
		if got := e.q.stopCalls[i]; got.TenantID != tid || got.MaxAgeDays != 9 {
			t.Fatalf("stop params %+v", got)
		}
	}
}

// "No data for this waybill yet" is a normal carrier answer: it backs off the
// shipment but never opens the breaker for every other shipment.
func TestShipmentTrackingNoTrackingDataDoesNotTripBreaker(t *testing.T) {
	e := newTrackingEnv()
	for i := 0; i < 5; i++ {
		e.addShipment(e.tenant, dbgen.ShipmentStatusSHIPPED, 0, e.now.Add(-time.Hour))
	}
	e.tracker.err = fmt.Errorf("%w: empty", shipping.ErrNoTrackingData)
	breaker := resilience.NewBreaker(2, 0.5, time.Hour)
	e.job.Provider = BreakerProvider{Inner: e.tracker, Breaker: breaker}
	res := e.run(t)
	if e.tracker.calls != 5 || res["skipped_breaker"] != 0 {
		t.Fatalf("calls=%d counters=%v", e.tracker.calls, res)
	}
	if res["no_tracking_data"] != 5 || res["errors"] != 0 {
		t.Fatalf("counters %v", res)
	}
	if len(e.q.updates) != 5 || e.q.updates[0].TrackAttempts != 1 {
		t.Fatalf("every shipment must be rescheduled with backoff, updates=%+v", e.q.updates)
	}
	if !breaker.Allow(context.Background()) {
		t.Fatal("breaker must stay closed")
	}
}
