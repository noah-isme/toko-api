package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// stubStore records CreateNotification calls and serves canned reads.
type stubStore struct {
	created   []dbgen.CreateNotificationParams
	createErr error

	listRows    []dbgen.Notification
	countTotal  int64
	countUnread int64

	markReadErr error
	markAllErr  error
	markedAll   bool
}

func (s *stubStore) CreateNotification(ctx context.Context, arg dbgen.CreateNotificationParams) (dbgen.Notification, error) {
	s.created = append(s.created, arg)
	if s.createErr != nil {
		return dbgen.Notification{}, s.createErr
	}
	return dbgen.Notification{ID: newUUID(), UserID: arg.UserID, TenantID: arg.TenantID, Type: arg.Type, Title: arg.Title, Body: arg.Body, Data: arg.Data}, nil
}

func (s *stubStore) ListNotifications(ctx context.Context, arg dbgen.ListNotificationsParams) ([]dbgen.Notification, error) {
	return s.listRows, nil
}

func (s *stubStore) CountNotifications(ctx context.Context, arg dbgen.CountNotificationsParams) (int64, error) {
	return s.countTotal, nil
}

func (s *stubStore) CountUnreadNotifications(ctx context.Context, arg dbgen.CountUnreadNotificationsParams) (int64, error) {
	return s.countUnread, nil
}

func (s *stubStore) MarkNotificationRead(ctx context.Context, arg dbgen.MarkNotificationReadParams) (pgtype.UUID, error) {
	if s.markReadErr != nil {
		return pgtype.UUID{}, s.markReadErr
	}
	return arg.ID, nil
}

func (s *stubStore) MarkAllNotificationsRead(ctx context.Context, arg dbgen.MarkAllNotificationsReadParams) error {
	s.markedAll = true
	return s.markAllErr
}

// stubOrders serves a single order keyed by id.
type stubOrders struct {
	order dbgen.Order
	err   error
}

func (s stubOrders) GetOrderByID(ctx context.Context, id pgtype.UUID) (dbgen.Order, error) {
	if s.err != nil {
		return dbgen.Order{}, s.err
	}
	return s.order, nil
}

func newUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

// validNow returns a fixed, valid timestamptz for deterministic tests.
func validNow() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true}
}

func TestNotifierCreatesForOrderTopic(t *testing.T) {
	userID := newUUID()
	tenantID := newUUID()
	orderID := newUUID()

	store := &stubStore{}
	n := &Notifier{
		Svc:    &Service{Q: store},
		Orders: stubOrders{order: dbgen.Order{ID: orderID, UserID: userID, TenantID: tenantID}},
	}

	ev := dbgen.DomainEvent{Topic: events.TopicOrderPaid, AggregateID: orderID, Payload: []byte("{}")}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(store.created))
	}
	got := store.created[0]
	if got.UserID != userID || got.TenantID != tenantID {
		t.Fatal("notification not addressed to order owner")
	}
	if got.Type != "order_paid" {
		t.Fatalf("unexpected type: %s", got.Type)
	}
}

func TestNotifierResolvesShipmentPayloadOrderID(t *testing.T) {
	userID := newUUID()
	tenantID := newUUID()
	orderID := newUUID()
	shipmentID := newUUID()

	store := &stubStore{}
	n := &Notifier{
		Svc:    &Service{Q: store},
		Orders: stubOrders{order: dbgen.Order{ID: orderID, UserID: userID, TenantID: tenantID}},
	}

	payload := []byte(`{"orderId":"` + uuid.UUID(orderID.Bytes).String() + `","status":"SHIPPED"}`)
	ev := dbgen.DomainEvent{Topic: events.TopicShipmentShipped, AggregateID: shipmentID, Payload: payload}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(store.created))
	}
	if store.created[0].Type != "shipment_shipped" {
		t.Fatalf("unexpected type: %s", store.created[0].Type)
	}
}

func TestNotifierSkipsUnmappedTopic(t *testing.T) {
	store := &stubStore{}
	n := &Notifier{Svc: &Service{Q: store}, Orders: stubOrders{}}

	ev := dbgen.DomainEvent{Topic: events.TopicOrderCreated, AggregateID: newUUID(), Payload: []byte("{}")}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(store.created) != 0 {
		t.Fatalf("expected no notification, got %d", len(store.created))
	}
}

func TestNotifierSwallowsOrderLookupError(t *testing.T) {
	var reported error
	store := &stubStore{}
	n := &Notifier{
		Svc:     &Service{Q: store},
		Orders:  stubOrders{err: pgx.ErrNoRows},
		OnError: func(err error) { reported = err },
	}

	ev := dbgen.DomainEvent{Topic: events.TopicOrderPaid, AggregateID: newUUID(), Payload: []byte("{}")}
	if err := n.Notify(context.Background(), ev); err != nil {
		t.Fatalf("notify should not surface lookup errors: %v", err)
	}
	if len(store.created) != 0 {
		t.Fatal("expected no notification when order lookup fails")
	}
	if reported == nil {
		t.Fatal("expected error to be reported via OnError")
	}
}
