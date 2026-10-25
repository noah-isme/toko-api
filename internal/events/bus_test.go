package events_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type stubStore struct {
	lastParams dbgen.InsertDomainEventParams
	event      dbgen.InsertDomainEventRow
}

func (s *stubStore) InsertDomainEvent(_ context.Context, arg dbgen.InsertDomainEventParams) (dbgen.InsertDomainEventRow, error) {
	s.lastParams = arg
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
}

func (c *captureNotifier) Notify(_ context.Context, event dbgen.DomainEvent) error {
	c.events = append(c.events, event)
	return nil
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

func TestEmitDoesNotStampTenantFromContext(t *testing.T) {
	store := &stubStore{}
	scheduler := &captureScheduler{}
	bus := events.Bus{Store: store, Scheduler: scheduler}

	// Payment callbacks run under the default tenant's context while the order
	// belongs to another tenant: Emit must stay a NULL-tenant broadcast.
	ctx := tenant.WithTenant(context.Background(), uuid.NewString())
	_, err := bus.Emit(ctx, events.TopicOrderPaid, toUUID(uuid.New()), map[string]any{"orderId": "1"})
	require.NoError(t, err)
	require.False(t, store.lastParams.TenantID.Valid)
	require.Len(t, scheduler.events, 1)
	require.False(t, scheduler.events[0].TenantID.Valid)
}

func TestEmitForTenantStampsTenant(t *testing.T) {
	store := &stubStore{}
	scheduler := &captureScheduler{}
	notifier := &captureNotifier{}
	bus := events.Bus{Store: store, Scheduler: scheduler, Notifiers: []events.Notifier{notifier}}

	tenantID := toUUID(uuid.New())
	event, err := bus.EmitForTenant(context.Background(), tenantID, events.TopicProductPriceChanged, toUUID(uuid.New()), map[string]any{"field": "price"})
	require.NoError(t, err)
	require.Equal(t, tenantID, store.lastParams.TenantID)
	require.Equal(t, tenantID, event.TenantID)
	require.Len(t, scheduler.events, 1)
	require.Equal(t, tenantID, scheduler.events[0].TenantID)
	require.Equal(t, tenantID, notifier.events[0].TenantID)
}
