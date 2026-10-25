package notify_test

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/queue"
)

// redispatchStore behaves like Postgres for EnqueueDelivery: the first insert
// for (endpoint, event) succeeds, later ones violate the unique constraint.
type redispatchStore struct {
	scheduleStore
	rows map[[2]pgtype.UUID]dbgen.WebhookDelivery
}

func (s *redispatchStore) EnqueueDelivery(_ context.Context, arg dbgen.EnqueueDeliveryParams) (dbgen.WebhookDelivery, error) {
	key := [2]pgtype.UUID{arg.EndpointID, arg.EventID}
	if _, ok := s.rows[key]; ok {
		return dbgen.WebhookDelivery{}, &pgconn.PgError{Code: "23505"}
	}
	d := dbgen.WebhookDelivery{ID: toUUID(uuid.New()), EndpointID: arg.EndpointID, EventID: arg.EventID, Status: dbgen.DeliveryStatusPENDING, MaxAttempt: arg.MaxAttempt}
	s.rows[key] = d
	return d, nil
}

func (s *redispatchStore) GetDeliveryByEndpointEvent(_ context.Context, arg dbgen.GetDeliveryByEndpointEventParams) (dbgen.WebhookDelivery, error) {
	return s.rows[[2]pgtype.UUID{arg.EndpointID, arg.EventID}], nil
}

func TestScheduleReenqueuesPendingDeliveryOnRedispatch(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	endpoint := dbgen.WebhookEndpoint{ID: toUUID(uuid.New())}
	store := &redispatchStore{
		scheduleStore: scheduleStore{endpoints: []dbgen.WebhookEndpoint{endpoint}},
		rows:          map[[2]pgtype.UUID]dbgen.WebhookDelivery{},
	}
	dispatcher := &notify.Dispatcher{
		Store:   store,
		Queue:   queue.Enqueuer{R: client, Prefix: "wh", DedupTTL: time.Minute},
		Enabled: true,
	}
	event := dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.paid"}
	ctx := context.Background()

	// First dispatch: the row is inserted but Redis rejects the enqueue.
	require.NoError(t, mr.Set("wh:queue:webhook-delivery", "broken"))
	require.Error(t, dispatcher.Schedule(ctx, event))
	delivery := store.rows[[2]pgtype.UUID{endpoint.ID, event.ID}]
	require.True(t, delivery.ID.Valid)

	// Relay re-dispatch after Redis recovered: the same delivery is enqueued.
	mr.Del("wh:queue:webhook-delivery")
	require.NoError(t, dispatcher.Schedule(ctx, event))
	members, err := client.ZRange(ctx, "wh:queue:webhook-delivery", 0, -1).Result()
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Contains(t, members[0], "\"key\":\""+uuidString(delivery.ID)+"\"")

	// A further re-dispatch is deduplicated by the delivery id.
	require.NoError(t, dispatcher.Schedule(ctx, event))
	n, err := client.ZCard(ctx, "wh:queue:webhook-delivery").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}

func TestScheduleSkipsRedispatchOfFinishedDelivery(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	endpoint := dbgen.WebhookEndpoint{ID: toUUID(uuid.New())}
	event := dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.paid"}
	store := &redispatchStore{
		scheduleStore: scheduleStore{endpoints: []dbgen.WebhookEndpoint{endpoint}},
		rows: map[[2]pgtype.UUID]dbgen.WebhookDelivery{
			{endpoint.ID, event.ID}: {ID: toUUID(uuid.New()), Status: dbgen.DeliveryStatusDELIVERED},
		},
	}
	dispatcher := &notify.Dispatcher{Store: store, Queue: queue.Enqueuer{R: client, Prefix: "wh"}, Enabled: true}

	require.NoError(t, dispatcher.Schedule(context.Background(), event))
	n, err := client.ZCard(context.Background(), "wh:queue:webhook-delivery").Result()
	require.NoError(t, err)
	require.Zero(t, n)
}
