package events_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

type stubOutboxStore struct {
	rows []dbgen.DomainEvent
	args []dbgen.ListUndispatchedDomainEventsParams
	err  error
}

func (s *stubOutboxStore) ListUndispatchedDomainEvents(_ context.Context, arg dbgen.ListUndispatchedDomainEventsParams) ([]dbgen.DomainEvent, error) {
	s.args = append(s.args, arg)
	return s.rows, s.err
}

type recordingLocker struct {
	keys []string
	ttls []time.Duration
	busy bool
	err  error
}

func (l *recordingLocker) TryLease(_ context.Context, key string, ttl time.Duration) (bool, error) {
	l.keys = append(l.keys, key)
	l.ttls = append(l.ttls, ttl)
	if l.err != nil {
		return false, l.err
	}
	return !l.busy, nil
}

func pendingEvent(topic string) dbgen.DomainEvent {
	return dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: topic, AggregateID: toUUID(uuid.New()), Payload: []byte(`{}`)}
}

func TestOutboxRelayRunOnceRedispatches(t *testing.T) {
	evA, evB := pendingEvent(events.TopicOrderPaid), pendingEvent(events.TopicShipmentShipped)
	outbox := &stubOutboxStore{rows: []dbgen.DomainEvent{evA, evB}}
	store := &stubStore{}
	notifier := &captureNotifier{}
	locker := &recordingLocker{}
	relay := &events.OutboxRelay{
		Store:  outbox,
		Bus:    &events.Bus{Store: store, Notifiers: []events.Notifier{notifier}},
		Locker: locker,
	}

	n, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Len(t, notifier.events, 2)
	require.ElementsMatch(t, []any{evA.ID, evB.ID}, []any{store.marked[0], store.marked[1]})
	require.Equal(t, []string{"lock:outbox:relay"}, locker.keys)
	require.Equal(t, []time.Duration{13500 * time.Millisecond}, locker.ttls, "lease is ~one default interval")
}

func TestOutboxRelayRunOnceCountsOnlySuccessfulDispatches(t *testing.T) {
	outbox := &stubOutboxStore{rows: []dbgen.DomainEvent{pendingEvent(events.TopicOrderPaid)}}
	store := &stubStore{}
	relay := &events.OutboxRelay{
		Store: outbox,
		Bus:   &events.Bus{Store: store, Notifiers: []events.Notifier{&captureNotifier{err: errors.New("still down")}}},
	}

	n, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Len(t, store.failures, 1)
}

func TestOutboxRelayRunOnceRespectsWindowAndLimits(t *testing.T) {
	outbox := &stubOutboxStore{}
	relay := &events.OutboxRelay{
		Store:       outbox,
		Bus:         &events.Bus{Store: &stubStore{}},
		MinAge:      30 * time.Second,
		MaxAge:      2 * time.Hour,
		MaxAttempts: 7,
		BatchSize:   50,
	}

	before := time.Now()
	_, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, outbox.args, 1)
	arg := outbox.args[0]
	require.Equal(t, int32(7), arg.MaxAttempts)
	require.Equal(t, int32(50), arg.BatchSize)
	require.ElementsMatch(t, events.DefaultTopics(), arg.Topics)
	require.WithinDuration(t, before.Add(-30*time.Second), arg.OlderThan.Time, 2*time.Second)
	require.WithinDuration(t, before.Add(-2*time.Hour), arg.NotBefore.Time, 2*time.Second)
}

func TestOutboxRelayRunOnceSkipsWhenLockBusy(t *testing.T) {
	outbox := &stubOutboxStore{rows: []dbgen.DomainEvent{pendingEvent(events.TopicOrderPaid)}}
	relay := &events.OutboxRelay{
		Store:  outbox,
		Bus:    &events.Bus{Store: &stubStore{}},
		Locker: &recordingLocker{busy: true},
	}
	n, err := relay.RunOnce(context.Background())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, outbox.args)
}

func TestOutboxRelayRunOnceReturnsLeaseError(t *testing.T) {
	outbox := &stubOutboxStore{rows: []dbgen.DomainEvent{pendingEvent(events.TopicOrderPaid)}}
	relay := &events.OutboxRelay{
		Store:  outbox,
		Bus:    &events.Bus{Store: &stubStore{}},
		Locker: &recordingLocker{err: errors.New("redis down")},
	}
	_, err := relay.RunOnce(context.Background())
	require.Error(t, err)
	require.Empty(t, outbox.args)
}

func TestOutboxRelayRunStopsOnCancel(t *testing.T) {
	relay := &events.OutboxRelay{Store: &stubOutboxStore{}, Bus: &events.Bus{Store: &stubStore{}}, Interval: 10 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.NoError(t, relay.Run(ctx))
}
