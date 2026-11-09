package shipping_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

func TestStatusTransitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	userID := uuid.New()

	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(userID), Status: dbgen.OrderStatusPAID}, "buyer@example.com")

	mailer := &recordingMailer{}
	svc := &shipping.Service{
		Q:                      queries,
		Mail:                   mailer,
		NotifyOnShipped:        true,
		NotifyOnOutForDelivery: true,
		NotifyOnDelivered:      true,
	}

	shipment, err := svc.Create(ctx, testTenantID, toPGUUID(orderID), "jne", "TRACK123")
	require.NoError(t, err)
	require.Equal(t, dbgen.ShipmentStatusPENDING, shipment.Status)
	require.Equal(t, dbgen.OrderStatusPACKED, queries.orders[orderID.String()].Status)

	for _, status := range []dbgen.ShipmentStatus{
		dbgen.ShipmentStatusSHIPPED,
		dbgen.ShipmentStatusOUTFORDELIVERY,
		dbgen.ShipmentStatusDELIVERED,
	} {
		_, _, err := svc.AppendEvent(ctx, toPGUUID(orderID), status, nil, nil, nil, []byte(`{"event":"ok"}`))
		require.NoError(t, err)
	}

	require.Len(t, queries.events, 3)
	require.Equal(t, dbgen.OrderStatusDELIVERED, queries.orders[orderID.String()].Status)
	require.Equal(t, dbgen.ShipmentStatusDELIVERED, queries.shipments[orderID.String()].Status)
	require.ElementsMatch(t, []string{"Pesanan dikirim", "Sedang dikirim", "Terkirim"}, mailer.Subjects())

	_, _, err = svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusOUTFORDELIVERY, nil, nil, nil, []byte(`{}`))
	require.ErrorIs(t, err, shipping.ErrInvalidShipmentTransition)
}

func TestCreateRejectsInvalidStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), Status: dbgen.OrderStatusPENDINGPAYMENT}, "")

	svc := &shipping.Service{Q: queries}
	_, err := svc.Create(ctx, testTenantID, toPGUUID(orderID), "jne", "TRACK123")
	require.ErrorIs(t, err, shipping.ErrOrderNotEligible)
}

func TestSyncOrderStatusIgnoresExpiredOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(uuid.New()), Status: dbgen.OrderStatusEXPIRED}, "buyer@example.com")
	queries.storeShipment(dbgen.Shipment{
		ID:         toPGUUID(uuid.New()),
		OrderID:    toPGUUID(orderID),
		Status:     dbgen.ShipmentStatusSHIPPED,
		LastStatus: dbgen.NullShipmentStatus{ShipmentStatus: dbgen.ShipmentStatusSHIPPED, Valid: true},
	})
	svc := &shipping.Service{Q: queries}

	_, _, err := svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusDELIVERED, nil, nil, nil, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, dbgen.OrderStatusEXPIRED, queries.orders[orderID.String()].Status)
	require.Empty(t, queries.statusUpdates, "an expired order must never be advanced")
}

func TestSyncOrderStatusStepsThroughSkippedStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(uuid.New()), Status: dbgen.OrderStatusSHIPPED}, "")
	queries.storeShipment(dbgen.Shipment{
		ID:         toPGUUID(uuid.New()),
		OrderID:    toPGUUID(orderID),
		Status:     dbgen.ShipmentStatusSHIPPED,
		LastStatus: dbgen.NullShipmentStatus{ShipmentStatus: dbgen.ShipmentStatusSHIPPED, Valid: true},
	})
	svc := &shipping.Service{Q: queries}

	_, _, err := svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusDELIVERED, nil, nil, nil, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, dbgen.OrderStatusDELIVERED, queries.orders[orderID.String()].Status)
	require.Equal(t, []dbgen.OrderStatus{dbgen.OrderStatusOUTFORDELIVERY, dbgen.OrderStatusDELIVERED}, queries.statusUpdates)
}

func TestAppendEventOptsSilentSkipsNotifyAndEmit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(uuid.New()), Status: dbgen.OrderStatusOUTFORDELIVERY}, "buyer@example.com")
	queries.storeShipment(dbgen.Shipment{
		ID:         toPGUUID(uuid.New()),
		OrderID:    toPGUUID(orderID),
		Status:     dbgen.ShipmentStatusOUTFORDELIVERY,
		LastStatus: dbgen.NullShipmentStatus{ShipmentStatus: dbgen.ShipmentStatusOUTFORDELIVERY, Valid: true},
	})
	mailer := &recordingMailer{}
	store := &recordingEventStore{}
	svc := &shipping.Service{Q: queries, Mail: mailer, NotifyOnDelivered: true, Events: &events.Bus{Store: store}}

	_, _, err := svc.AppendEventOpts(ctx, toPGUUID(orderID), dbgen.ShipmentStatusDELIVERED, nil, nil, nil, []byte(`{}`), shipping.AppendOptions{Silent: true})
	require.NoError(t, err)
	require.Len(t, queries.events, 1)
	require.Equal(t, dbgen.OrderStatusDELIVERED, queries.orders[orderID.String()].Status)
	require.Empty(t, mailer.Subjects())
	require.Empty(t, store.topics)

	// Non-silent appends still notify (same-status event is allowed).
	_, _, err = svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusDELIVERED, nil, nil, nil, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Terkirim"}, mailer.Subjects())
	require.Equal(t, []string{events.TopicShipmentDelivered}, store.topics)
}

type recordingEventStore struct {
	topics []string
}

func (r *recordingEventStore) InsertDomainEvent(_ context.Context, arg dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error) {
	r.topics = append(r.topics, arg.Topic)
	return dbgen.DomainEvent{ID: arg.AggregateID, Topic: arg.Topic, AggregateID: arg.AggregateID, Payload: arg.Payload}, nil
}

func (*recordingEventStore) MarkDomainEventDispatched(context.Context, pgtype.UUID) error {
	return nil
}

func (*recordingEventStore) RecordDomainEventDispatchFailure(context.Context, dbgen.RecordDomainEventDispatchFailureParams) error {
	return nil
}

func TestAppendEventOptsRequireForwardRejectsSameStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(uuid.New()), Status: dbgen.OrderStatusDELIVERED}, "buyer@example.com")
	queries.storeShipment(dbgen.Shipment{
		ID:         toPGUUID(uuid.New()),
		OrderID:    toPGUUID(orderID),
		Status:     dbgen.ShipmentStatusDELIVERED,
		LastStatus: dbgen.NullShipmentStatus{ShipmentStatus: dbgen.ShipmentStatusDELIVERED, Valid: true},
	})
	mailer := &recordingMailer{}
	store := &recordingEventStore{}
	svc := &shipping.Service{Q: queries, Mail: mailer, NotifyOnDelivered: true, Events: &events.Bus{Store: store}}

	// A second tracking run that saw the same DELIVERED scan must not record
	// it again or notify twice.
	_, _, err := svc.AppendEventOpts(ctx, toPGUUID(orderID), dbgen.ShipmentStatusDELIVERED, nil, nil, nil, []byte(`{}`), shipping.AppendOptions{RequireForward: true})
	require.ErrorIs(t, err, shipping.ErrInvalidShipmentTransition)
	require.Empty(t, queries.events)
	require.Empty(t, mailer.Subjects())
	require.Empty(t, store.topics)
}

func TestCreateRejectsOrderOfOtherTenant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), Status: dbgen.OrderStatusPAID}, "")

	svc := &shipping.Service{Q: queries}
	otherTenant := toPGUUID(uuid.New())
	_, err := svc.Create(ctx, otherTenant, toPGUUID(orderID), "jne", "TRACK123")
	require.ErrorIs(t, err, pgx.ErrNoRows)
	require.Empty(t, queries.shipments, "no shipment may be created for another tenant's order")
	require.Equal(t, dbgen.OrderStatusPAID, queries.orders[orderID.String()].Status)
}

func TestCreateRequiresTenant(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), Status: dbgen.OrderStatusPAID}, "")

	svc := &shipping.Service{Q: queries}
	_, err := svc.Create(context.Background(), pgtype.UUID{}, toPGUUID(orderID), "jne", "TRACK123")
	require.Error(t, err)
	require.Empty(t, queries.shipments)
}
