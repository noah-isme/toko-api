package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/textproto"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/queue"
)

const recipient = "alice.customer@example.com"

type stubEmailEnqueuer struct {
	tasks []notify.EmailTask
	keys  []string
	err   error
}

func (s *stubEmailEnqueuer) EnqueueEmail(_ context.Context, task notify.EmailTask, key string) error {
	s.tasks = append(s.tasks, task)
	s.keys = append(s.keys, key)
	return s.err
}

type stubEventLoader struct {
	event dbgen.DomainEvent
	err   error
}

func (s stubEventLoader) GetDomainEvent(context.Context, pgtype.UUID) (dbgen.DomainEvent, error) {
	return s.event, s.err
}

type failingMailer struct{ err error }

func (f failingMailer) Send(string, string, string) error { return f.err }

func orderEvent(topic string, payload string) dbgen.DomainEvent {
	return dbgen.DomainEvent{
		ID:          toUUID(uuid.New()),
		Topic:       topic,
		AggregateID: toUUID(uuid.New()),
		Payload:     []byte(payload),
		OccurredAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

func TestEmailNotifierEnqueuesOpaqueTaskWithEventKey(t *testing.T) {
	enq := &stubEmailEnqueuer{}
	n := notify.EmailNotifier{Queue: enq, Enabled: true}
	ev := orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`","orderId":"o-1"}`)

	require.NoError(t, n.Notify(context.Background(), ev))
	require.Len(t, enq.tasks, 1)
	id := uuidString(ev.ID)
	require.Equal(t, notify.EmailTask{EventID: id, Topic: events.TopicOrderPaid}, enq.tasks[0])
	require.Equal(t, "email:"+id, enq.keys[0])

	// DLQ-leak guard: the task must not carry the recipient or the copy.
	raw, err := json.Marshal(enq.tasks[0])
	require.NoError(t, err)
	require.NotContains(t, string(raw), recipient)
	require.NotContains(t, string(raw), "Pembayaran berhasil")
	require.JSONEq(t, `{"eventId":"`+id+`","topic":"order.paid"}`, string(raw))
}

func TestEmailNotifierSkipsDisabledTopic(t *testing.T) {
	enq := &stubEmailEnqueuer{}
	n := notify.EmailNotifier{Queue: enq, Enabled: true, TopicToggles: map[string]bool{events.TopicOrderPaid: false}}
	require.NoError(t, n.Notify(context.Background(), orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`"}`)))
	require.Empty(t, enq.tasks)

	disabled := notify.EmailNotifier{Queue: enq, Enabled: false}
	require.NoError(t, disabled.Notify(context.Background(), orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`"}`)))
	require.Empty(t, enq.tasks)
}

func TestEmailNotifierSkipsWithoutRecipient(t *testing.T) {
	enq := &stubEmailEnqueuer{}
	n := notify.EmailNotifier{Queue: enq, Enabled: true}
	require.NoError(t, n.Notify(context.Background(), orderEvent(events.TopicOrderPaid, `{"orderId":"o-1"}`)))
	require.Empty(t, enq.tasks)
}

func TestEmailNotifierReturnsEnqueueError(t *testing.T) {
	enq := &stubEmailEnqueuer{err: errors.New("redis down")}
	n := notify.EmailNotifier{Queue: enq, Enabled: true}
	require.Error(t, n.Notify(context.Background(), orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`"}`)))
}

func TestMergeShipmentTogglesAndsNotifyOnFlags(t *testing.T) {
	in := map[string]bool{events.TopicShipmentDelivered: false, events.TopicOrderPaid: true}
	got := notify.MergeShipmentToggles(in, true, false, true)
	require.True(t, got[events.TopicShipmentShipped], "absent topic defaults on and flag is on")
	require.False(t, got[events.TopicShipmentOutForDelivery], "NOTIFY_ON_OUT_FOR_DELIVERY=false wins")
	require.False(t, got[events.TopicShipmentDelivered], "topic toggle off wins over flag on")
	require.True(t, got[events.TopicOrderPaid])
	require.Len(t, in, 2, "input map must not be modified")
}

func TestRenderEmailShipmentBodyUsesShippingCopy(t *testing.T) {
	ev := orderEvent(events.TopicShipmentShipped, `{"email":"`+recipient+`","orderId":"o-1","shipmentId":"s-1"}`)
	to, subject, body, ok := notify.RenderEmail(ev)
	require.True(t, ok)
	require.Equal(t, recipient, to)
	require.Equal(t, "Pesanan dikirim", subject)
	require.True(t, strings.HasPrefix(body, "Pesanan Anda telah dikirim.\n"), body)
	require.Contains(t, body, "ID Pesanan: o-1")
	require.Contains(t, body, "ID Pengiriman: s-1")

	_, _, delivered, _ := notify.RenderEmail(orderEvent(events.TopicShipmentDelivered, `{"email":"`+recipient+`"}`))
	require.True(t, strings.HasPrefix(delivered, "Pesanan Anda telah diterima."))
}

func TestEmailQueueEnqueuesToRedis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	q := &notify.EmailQueue{Queue: queue.Enqueuer{R: client, Prefix: "q", DedupTTL: time.Minute}, MaxAttempts: 5}
	task := notify.EmailTask{EventID: uuid.NewString(), Topic: events.TopicOrderPaid}
	require.NoError(t, q.EnqueueEmail(context.Background(), task, "email:"+task.EventID))
	require.NoError(t, q.EnqueueEmail(context.Background(), task, "email:"+task.EventID), "duplicate is deduplicated, not an error")

	members, err := client.ZRange(context.Background(), "q:queue:email-delivery", 0, -1).Result()
	require.NoError(t, err)
	require.Len(t, members, 1)
	var msg struct {
		Payload     []byte `json:"payload"`
		MaxAttempts int    `json:"max_attempts"`
	}
	require.NoError(t, json.Unmarshal([]byte(members[0]), &msg))
	require.Equal(t, 5, msg.MaxAttempts)
	var decoded notify.EmailTask
	require.NoError(t, json.Unmarshal(msg.Payload, &decoded))
	require.Equal(t, task, decoded)

	var nilQueue *notify.EmailQueue
	require.ErrorIs(t, nilQueue.EnqueueEmail(context.Background(), task, "k"), notify.ErrEmailQueueUnavailable)
}

func taskPayload(t *testing.T, ev dbgen.DomainEvent) []byte {
	t.Helper()
	raw, err := json.Marshal(notify.EmailTask{EventID: uuidString(ev.ID), Topic: ev.Topic})
	require.NoError(t, err)
	return raw
}

func TestEmailWorkerRendersAndSendsViaMailer(t *testing.T) {
	ev := orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`","orderId":"o-1"}`)
	mail := &common.InMemoryEmail{}
	w := &notify.EmailWorker{Events: stubEventLoader{event: ev}, Mail: mail, Enabled: true}

	require.NoError(t, w.Handle(context.Background(), taskPayload(t, ev)))
	require.Len(t, mail.Outbox, 1)
	require.Equal(t, recipient, mail.Outbox[0].To)
	require.Equal(t, "Pembayaran berhasil", mail.Outbox[0].Subject)
	require.Contains(t, mail.Outbox[0].HTML, "ID Pesanan: o-1")
}

func TestEmailWorkerDropsMissingEvent(t *testing.T) {
	ev := orderEvent(events.TopicOrderPaid, `{}`)
	mail := &common.InMemoryEmail{}
	w := &notify.EmailWorker{Events: stubEventLoader{err: pgx.ErrNoRows}, Mail: mail, Enabled: true}
	require.NoError(t, w.Handle(context.Background(), taskPayload(t, ev)))
	require.Empty(t, mail.Outbox)
}

func TestEmailWorkerRetriesOnEventLoadError(t *testing.T) {
	ev := orderEvent(events.TopicOrderPaid, `{}`)
	w := &notify.EmailWorker{Events: stubEventLoader{err: errors.New("db down")}, Mail: &common.InMemoryEmail{}, Enabled: true}
	require.Error(t, w.Handle(context.Background(), taskPayload(t, ev)))
}

func TestEmailWorkerReturnsRedactedErrorWhenSendFails(t *testing.T) {
	ev := orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`"}`)
	cases := map[string]error{
		// A 4xx RCPT reply that echoes the address: retryable.
		"rcpt 451": &common.SendError{Stage: "rcpt to", Err: &textproto.Error{Code: 451, Msg: "4.2.0 <" + recipient + "> mailbox busy"}},
		// DATA rejection quoting the recipient.
		"data 554": &common.SendError{Stage: "close body", Err: &textproto.Error{Code: 554, Msg: "rejected for " + recipient}},
		"dial":     &common.SendError{Stage: "dial", Addr: "smtp.example.com:587", Err: errors.New("connection refused")},
		// An arbitrary sender whose error text embeds the address.
		"opaque": errors.New("send to " + recipient + " failed"),
	}
	for name, sendErr := range cases {
		t.Run(name, func(t *testing.T) {
			w := &notify.EmailWorker{Events: stubEventLoader{event: ev}, Mail: failingMailer{err: sendErr}, Enabled: true, RelayHost: "smtp.example.com"}
			err := w.Handle(context.Background(), taskPayload(t, ev))
			require.Error(t, err)
			require.NotContains(t, err.Error(), recipient)
			require.NotContains(t, err.Error(), "alice")
			require.Contains(t, err.Error(), "smtp.example.com")
		})
	}
}

func TestEmailWorkerDropsPermanentRecipientFailures(t *testing.T) {
	ev := orderEvent(events.TopicOrderPaid, `{"email":"`+recipient+`"}`)
	for name, sendErr := range map[string]error{
		"invalid recipient": common.ErrInvalidRecipient,
		"rcpt 550":          &common.SendError{Stage: "rcpt to", Err: &textproto.Error{Code: 550, Msg: "5.1.1 <" + recipient + "> unknown user"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := &notify.EmailWorker{Events: stubEventLoader{event: ev}, Mail: failingMailer{err: sendErr}, Enabled: true}
			require.NoError(t, w.Handle(context.Background(), taskPayload(t, ev)), "permanent failures are dropped, not retried into the DLQ")
		})
	}
}

func TestEmailWorkerRejectsInvalidPayload(t *testing.T) {
	w := &notify.EmailWorker{Events: stubEventLoader{}, Mail: &common.InMemoryEmail{}, Enabled: true}
	require.Error(t, w.Handle(context.Background(), []byte("not json")))
	require.Error(t, w.Handle(context.Background(), []byte(`{"eventId":"nope","topic":"order.paid"}`)))
}

func TestEmailWorkerHonoursTopicToggle(t *testing.T) {
	ev := orderEvent(events.TopicShipmentShipped, `{"email":"`+recipient+`"}`)
	mail := &common.InMemoryEmail{}
	w := &notify.EmailWorker{
		Events:       stubEventLoader{event: ev},
		Mail:         mail,
		Enabled:      true,
		TopicToggles: notify.MergeShipmentToggles(nil, false, true, true),
	}
	require.NoError(t, w.Handle(context.Background(), taskPayload(t, ev)))
	require.Empty(t, mail.Outbox)

	w.TopicToggles = nil
	w.Enabled = false
	require.NoError(t, w.Handle(context.Background(), taskPayload(t, ev)))
	require.Empty(t, mail.Outbox)
}
