package queue_test

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/queue"
)

func TestEnqueueFailedZAddLeavesNoDedupKey(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	enq := queue.Enqueuer{R: client, Prefix: "dd", DedupTTL: 10 * time.Minute}
	ctx := context.Background()
	task := queue.Task{Kind: "email-delivery", Payload: []byte(`{"eventId":"e1"}`), IdempotencyKey: "email:e1"}

	// A wrong-typed queue key makes ZADD fail with WRONGTYPE.
	require.NoError(t, mr.Set("dd:queue:email-delivery", "not-a-zset"))
	require.Error(t, enq.Enqueue(ctx, task))
	require.False(t, mr.Exists("dd:dedup:email-delivery:email:e1"), "a failed ZADD must not leave the dedup key behind")

	// Once Redis is healthy again the same task (same key) is queued.
	mr.Del("dd:queue:email-delivery")
	require.NoError(t, enq.Enqueue(ctx, task))
	n, err := client.ZCard(ctx, "dd:queue:email-delivery").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	require.True(t, mr.Exists("dd:dedup:email-delivery:email:e1"))

	// A duplicate inside the window is still dropped.
	require.NoError(t, enq.Enqueue(ctx, task))
	n, err = client.ZCard(ctx, "dd:queue:email-delivery").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	ttl := mr.TTL("dd:dedup:email-delivery:email:e1")
	require.Greater(t, ttl, 9*time.Minute)
}
