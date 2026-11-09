package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type stubStore struct {
	lastParams dbgen.InsertDomainEventParams
	event      dbgen.DomainEvent
	insertErr  error
	markErr    error
	marked     []pgtype.UUID
	failures   []dbgen.RecordDomainEventDispatchFailureParams
}

func (s *stubStore) MarkDomainEventDispatched(_ context.Context, id pgtype.UUID) error {
	s.marked = append(s.marked, id)
	return s.markErr
}

func (s *stubStore) RecordDomainEventDispatchFailure(_ context.Context, arg dbgen.RecordDomainEventDispatchFailureParams) error {
	s.failures = append(s.failures, arg)
	return nil
}

func (s *stubStore) InsertDomainEvent(_ context.Context, arg dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error) {
	s.lastParams = arg
	if s.insertErr != nil {
		return dbgen.DomainEvent{}, s.insertErr
	}
	if !s.event.ID.Valid {
		id := uuid.New()
		s.event.ID = pgtype.UUID{Bytes: id, Valid: true}
	}
	s.event.Topic = arg.Topic
	s.event.AggregateID = arg.AggregateID
	s.event.Payload = arg.Payload
	if !s.event.OccurredAt.Valid {
		s.event.OccurredAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	return s.event, nil
}

type captureScheduler struct {
	events []dbgen.DomainEvent
}

func (c *captureScheduler) Schedule(_ context.Context, event dbgen.DomainEvent) error {
	c.events = append(c.events, event)
	return nil
}

type captureNotifier struct {
	events []dbgen.DomainEvent
	err    error
}

func (c *captureNotifier) Notify(_ context.Context, event dbgen.DomainEvent) error {
	c.events = append(c.events, event)
	return c.err
}

func toUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func TestEmitPersistsEvent(t *testing.T) {
	store := &stubStore{}
	scheduler := &captureScheduler{}
	notifier := &captureNotifier{}
	bus := events.Bus{
		Store:     store,
		Scheduler: scheduler,
		Notifiers: []events.Notifier{notifier},
	}

	aggregate := uuid.New()
	payload := map[string]any{"orderId": "123"}
	ctx := context.Background()
	event, err := bus.Emit(ctx, events.TopicOrderCreated, toUUID(aggregate), payload)
	require.NoError(t, err)
	require.Equal(t, events.TopicOrderCreated, store.lastParams.Topic)
	require.JSONEq(t, `{"orderId":"123"}`, string(store.lastParams.Payload))
	require.Len(t, scheduler.events, 1)
	require.Len(t, notifier.events, 1)
	require.Equal(t, event.ID, scheduler.events[0].ID)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(event.Payload, &decoded))
	require.Equal(t, "123", decoded["orderId"])
}

func TestEmitMarksDispatchedOnSuccess(t *testing.T) {
	store := &stubStore{}
	bus := events.Bus{Store: store, Notifiers: []events.Notifier{&captureNotifier{}}}

	ev, err := bus.Emit(context.Background(), events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{ev.ID}, store.marked)
	require.Empty(t, store.failures)
}

func TestEmitRecordsFailureWhenNotifierFails(t *testing.T) {
	store := &stubStore{}
	failing := &captureNotifier{err: errors.New("redis down")}
	ok := &captureNotifier{}
	bus := events.Bus{Store: store, Notifiers: []events.Notifier{failing, ok}}

	ev, err := bus.Emit(context.Background(), events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.Error(t, err)
	require.False(t, errors.Is(err, events.ErrPersist), "row was stored, so this is not a persist error")
	require.True(t, ev.ID.Valid)
	require.Len(t, ok.events, 1, "one failing notifier must not starve the others")
	require.Empty(t, store.marked)
	require.Len(t, store.failures, 1)
	require.Equal(t, ev.ID, store.failures[0].ID)
	require.Contains(t, store.failures[0].LastDispatchError.String, "redis down")
}

func TestEmitTruncatesStoredDispatchError(t *testing.T) {
	store := &stubStore{}
	bus := events.Bus{Store: store, Notifiers: []events.Notifier{&captureNotifier{err: errors.New(strings.Repeat("x", 5000))}}}

	_, err := bus.Emit(context.Background(), events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.Error(t, err)
	require.Len(t, store.failures[0].LastDispatchError.String, 2000)
}

func TestEmitWrapsInsertFailureWithErrPersist(t *testing.T) {
	store := &stubStore{insertErr: errors.New("db down")}
	notifier := &captureNotifier{}
	bus := events.Bus{Store: store, Notifiers: []events.Notifier{notifier}}

	_, err := bus.Emit(context.Background(), events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.ErrorIs(t, err, events.ErrPersist)
	require.Empty(t, notifier.events)
}

func TestEmitReturnsMarkFailure(t *testing.T) {
	store := &stubStore{markErr: errors.New("mark failed")}
	bus := events.Bus{Store: store}

	_, err := bus.Emit(context.Background(), events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.Error(t, err)
	require.False(t, errors.Is(err, events.ErrPersist))
}

func TestEmitLoggedSwallowsErrorsAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	ctx := logger.WithContext(context.Background())

	failing := events.Bus{Store: &stubStore{}, Notifiers: []events.Notifier{&captureNotifier{err: errors.New("boom")}}}
	failing.EmitLogged(ctx, events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.Contains(t, buf.String(), `"level":"warn"`)
	require.Contains(t, buf.String(), "outbox relay will retry")

	buf.Reset()
	lost := events.Bus{Store: &stubStore{insertErr: errors.New("db down")}}
	lost.EmitLogged(ctx, events.TopicOrderPaid, toUUID(uuid.New()), nil)
	require.Contains(t, buf.String(), `"level":"error"`)
	require.Contains(t, buf.String(), "not persisted")
}

// ctxCheckingStore fails the insert when its context is already cancelled, like
// pgx does.
type ctxCheckingStore struct{ stubStore }

func (s *ctxCheckingStore) InsertDomainEvent(ctx context.Context, arg dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error) {
	if err := ctx.Err(); err != nil {
		return dbgen.DomainEvent{}, err
	}
	return s.stubStore.InsertDomainEvent(ctx, arg)
}

func TestEmitLoggedIgnoresCallerCancellation(t *testing.T) {
	store := &ctxCheckingStore{}
	notifier := &captureNotifier{}
	bus := events.Bus{Store: store, Notifiers: []events.Notifier{notifier}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client disconnected right after the business commit
	bus.EmitLogged(ctx, events.TopicOrderPaid, toUUID(uuid.New()), nil)

	require.Len(t, notifier.events, 1)
	require.Len(t, store.marked, 1)
}

func TestOrderIDForShipmentTopicUsesPayload(t *testing.T) {
	orderID := uuid.New()
	ev := dbgen.DomainEvent{
		Topic:       events.TopicShipmentShipped,
		AggregateID: toUUID(uuid.New()),
		Payload:     []byte(`{"orderId":"` + orderID.String() + `"}`),
	}
	got, ok := events.OrderIDFor(ev)
	require.True(t, ok)
	require.Equal(t, toUUID(orderID), got)

	ev.Payload = []byte(`{}`)
	_, ok = events.OrderIDFor(ev)
	require.False(t, ok)

	agg := toUUID(uuid.New())
	got, ok = events.OrderIDFor(dbgen.DomainEvent{Topic: events.TopicOrderPaid, AggregateID: agg})
	require.True(t, ok)
	require.Equal(t, agg, got)
}

func TestDeliverDoesNotPersistAgain(t *testing.T) {
	store := &stubStore{}
	notifier := &captureNotifier{}
	bus := &events.Bus{Store: store, Notifiers: []events.Notifier{notifier}}
	ev := dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: events.TopicAlertBackInStock, AggregateID: toUUID(uuid.New())}
	require.NoError(t, bus.Deliver(context.Background(), ev))
	require.Empty(t, store.lastParams.Topic, "Deliver must not insert a domain event")
	require.Len(t, notifier.events, 1)
	require.Equal(t, ev.ID, notifier.events[0].ID)
	require.Equal(t, []pgtype.UUID{ev.ID}, store.marked, "a delivered event is marked dispatched")

	var nilBus *events.Bus
	require.Error(t, nilBus.Deliver(context.Background(), ev))
}

func TestRelayTopicsExcludeAlertTopics(t *testing.T) {
	topics := events.RelayTopics()
	require.NotContains(t, topics, events.TopicAlertBackInStock)
	require.NotContains(t, topics, events.TopicAlertPriceDrop)
	require.Contains(t, topics, events.TopicOrderPaid)
	require.Contains(t, topics, events.TopicProductStockChanged)
	require.Len(t, topics, len(events.DefaultTopics())-2+3)
}

func TestEmitForTenantStampsTenant(t *testing.T) {
	store := &stubStore{}
	scheduler := &captureScheduler{}
	notifier := &captureNotifier{}
	bus := events.Bus{Store: store, Scheduler: scheduler, Notifiers: []events.Notifier{notifier}}

	// The explicit tenant wins over the ctx tenant.
	ctx := tenant.WithTenant(context.Background(), uuid.NewString())
	tenantID := toUUID(uuid.New())
	_, err := bus.EmitForTenant(ctx, tenantID, events.TopicProductPriceChanged, toUUID(uuid.New()), map[string]any{"field": "price"})
	require.NoError(t, err)
	require.Equal(t, tenantID, store.lastParams.TenantID)
}
