package push

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/queue"
)

type stubTaskEnqueuer struct {
	tasks []Task
	keys  []string
}

func (s *stubTaskEnqueuer) EnqueueTask(_ context.Context, task Task, key string) error {
	s.tasks = append(s.tasks, task)
	s.keys = append(s.keys, key)
	return nil
}

type stubEvents struct {
	event dbgen.DomainEvent
	err   error
}

func (s stubEvents) GetDomainEvent(context.Context, pgtype.UUID) (dbgen.DomainEvent, error) {
	return s.event, s.err
}

type stubOrders struct {
	order dbgen.Order
	err   error
	asked []pgtype.UUID
}

func (s *stubOrders) GetOrderByID(_ context.Context, id pgtype.UUID) (dbgen.Order, error) {
	s.asked = append(s.asked, id)
	return s.order, s.err
}

func TestNotifierEnqueuesOpaqueTask(t *testing.T) {
	enq := &stubTaskEnqueuer{}
	n := &Notifier{Enqueue: enq, Enabled: true}
	ev := dbgen.DomainEvent{ID: newID(), Topic: events.TopicOrderPaid, AggregateID: newID(), Payload: []byte(`{"userId":"u-1","email":"a@b.c"}`)}
	require.NoError(t, n.Notify(context.Background(), ev))
	require.Equal(t, []Task{{EventID: uuidString(ev.ID), Topic: events.TopicOrderPaid}}, enq.tasks)
	require.Equal(t, []string{"push:" + uuidString(ev.ID)}, enq.keys)
	raw, err := json.Marshal(enq.tasks[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "userId")
	require.NotContains(t, string(raw), "a@b.c")
}

func TestNotifierIgnoresUnknownTopic(t *testing.T) {
	enq := &stubTaskEnqueuer{}
	n := &Notifier{Enqueue: enq, Enabled: true}
	require.NoError(t, n.Notify(context.Background(), dbgen.DomainEvent{ID: newID(), Topic: events.TopicOrderCreated}))
	require.Empty(t, enq.tasks)
}

func TestNotifierNoopWhenDisabled(t *testing.T) {
	enq := &stubTaskEnqueuer{}
	n := &Notifier{Enqueue: enq, Enabled: false}
	require.NoError(t, n.Notify(context.Background(), dbgen.DomainEvent{ID: newID(), Topic: events.TopicOrderPaid}))
	require.Empty(t, enq.tasks)
}

func TestEnqueuerWritesToRedis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	enq := &Enqueuer{Queue: queue.Enqueuer{R: client, Prefix: "p", DedupTTL: time.Minute}}
	task := Task{EventID: uuid.NewString(), Topic: events.TopicOrderPaid}
	require.NoError(t, enq.EnqueueTask(context.Background(), task, "push:"+task.EventID))
	n, err := client.ZCard(context.Background(), "p:queue:push-delivery").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	var nilEnq *Enqueuer
	require.ErrorIs(t, nilEnq.EnqueueTask(context.Background(), task, "k"), ErrQueueUnavailable)
}

func TestBuildMessageOrderPaid(t *testing.T) {
	orderID := newID()
	msg, category, ok := BuildMessage(dbgen.DomainEvent{Topic: events.TopicOrderPaid}, orderID)
	require.True(t, ok)
	require.Equal(t, CategoryOrderUpdate, category)
	id := uuidString(orderID)
	require.Equal(t, "Pembayaran diterima", msg.Title)
	require.Equal(t, "order:"+id, msg.Tag)
	require.Equal(t, "/account/orders/"+id, msg.Data["url"])
	require.Equal(t, id, msg.Data["orderId"])
	require.Equal(t, events.TopicOrderPaid, msg.Data["topic"])

	_, _, ok = BuildMessage(dbgen.DomainEvent{Topic: events.TopicOrderCreated}, orderID)
	require.False(t, ok)
}

func TestBuildMessageShipmentTopics(t *testing.T) {
	for _, topic := range []string{events.TopicShipmentShipped, events.TopicShipmentOutForDelivery, events.TopicShipmentDelivered} {
		msg, category, ok := BuildMessage(dbgen.DomainEvent{Topic: topic}, newID())
		require.True(t, ok, topic)
		require.Equal(t, CategoryOrderUpdate, category)
		require.NotEmpty(t, msg.Title)
	}
}

type workerFixture struct {
	store  *stubStore
	sender *fakeSender
	orders *stubOrders
	worker *Worker
	event  dbgen.DomainEvent
	user   pgtype.UUID
	order  pgtype.UUID
}

func newWorkerFixture(topic string) *workerFixture {
	user, orderID := newID(), newID()
	f := &workerFixture{
		store:  &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint)}},
		sender: &fakeSender{},
		orders: &stubOrders{order: dbgen.Order{ID: orderID, UserID: user}},
		user:   user,
		order:  orderID,
	}
	f.event = dbgen.DomainEvent{ID: newID(), Topic: topic, AggregateID: orderID, Payload: []byte(`{}`)}
	f.worker = &Worker{Svc: enabledService(f.store, f.sender), Events: stubEvents{event: f.event}, Orders: f.orders}
	return f
}

func (f *workerFixture) payload(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(Task{EventID: uuidString(f.event.ID), Topic: f.event.Topic})
	require.NoError(t, err)
	return raw
}

func TestWorkerRendersFromEventAndSends(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)))
	require.Len(t, f.sender.sent, 1)
	require.Equal(t, f.user, f.sender.sent[0].sub.UserID)
	require.Equal(t, "/account/orders/"+uuidString(f.order), f.sender.sent[0].msg.Data["url"])
}

func TestWorkerUsesPayloadOrderIDForShipmentTopics(t *testing.T) {
	f := newWorkerFixture(events.TopicShipmentShipped)
	f.event.AggregateID = newID() // shipment id
	f.event.Payload = []byte(`{"orderId":"` + uuidString(f.order) + `"}`)
	f.worker.Events = stubEvents{event: f.event}
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)))
	require.Equal(t, []pgtype.UUID{f.order}, f.orders.asked)
	require.Len(t, f.sender.sent, 1)
}

func TestWorkerSkipsWhenPreferenceDisabled(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	f.store.prefs = &dbgen.PushPreference{Enabled: true, OrderUpdates: false}
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)))
	require.Empty(t, f.sender.sent)
}

func TestWorkerDropsMissingEvent(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	f.worker.Events = stubEvents{err: pgx.ErrNoRows}
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)))
	require.Empty(t, f.sender.sent)

	f.worker.Events = stubEvents{err: errors.New("db down")}
	require.Error(t, f.worker.Handle(context.Background(), f.payload(t)), "transient load errors are retried")
}

func TestWorkerReturnsErrorOnRetryableFailure(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	f.sender.statuses = map[string]int{fcmEndpoint: 503}
	err := f.worker.Handle(context.Background(), f.payload(t))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-token", "DLQ last_error must not contain the endpoint")
	require.NotContains(t, err.Error(), uuidString(f.user))
}

func TestWorkerDropsWhenPushDisabled(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	f.worker.Svc = &Service{Q: f.store}
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)))
	require.Empty(t, f.sender.sent)
}

func TestWorkerIgnoresNoSubscriptions(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	f.store.subs = nil
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)))
}

func TestWorkerRejectsInvalidPayload(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	require.Error(t, f.worker.Handle(context.Background(), []byte("{")))
	require.Error(t, f.worker.Handle(context.Background(), []byte(`{"eventId":"x"}`)))
}

func TestWorkerTreatsPartialSuccessAsDone(t *testing.T) {
	f := newWorkerFixture(events.TopicOrderPaid)
	f.store.subs = append(f.store.subs, subFor(f.user, mozillaEndpoint))
	f.sender.statuses = map[string]int{mozillaEndpoint: 503}
	require.NoError(t, f.worker.Handle(context.Background(), f.payload(t)),
		"a retry would resend to the subscription that already got the push")
	require.Len(t, f.sender.sent, 2)
}
