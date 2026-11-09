package queue_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/queue"
)

func runOneTask(t *testing.T, doneMarkerTTL time.Duration) (*miniredis.Miniredis, queue.Enqueuer, *atomic.Int32) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	enq := queue.Enqueuer{R: client, Prefix: "dm", DedupTTL: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, enq.Enqueue(ctx, queue.Task{Kind: "demo", Payload: []byte("p"), IdempotencyKey: "ev-1"}))

	var runs atomic.Int32
	worker := queue.Worker{
		R: client, Prefix: "dm", Kind: "demo", Concurrency: 1,
		VisibilityTimeout: time.Second, RetryBase: 5 * time.Millisecond,
		DoneMarkerTTL: doneMarkerTTL,
		Handler: func(context.Context, queue.Task) error {
			runs.Add(1)
			return nil
		},
	}
	go func() { _ = worker.Run(ctx) }()
	require.Eventually(t, func() bool { return runs.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	return mr, enq, &runs
}

func TestWorkerDoneMarkerDropsReenqueueAfterAck(t *testing.T) {
	mr, enq, runs := runOneTask(t, 24*time.Hour)
	require.Eventually(t, func() bool {
		v, err := mr.Get("dm:dedup:demo:ev-1")
		return err == nil && v == "done"
	}, 2*time.Second, 5*time.Millisecond, "ack must keep the dedup key as a done marker")
	require.Greater(t, mr.TTL("dm:dedup:demo:ev-1"), time.Hour, "the marker outlives the enqueue dedup TTL")

	require.NoError(t, enq.Enqueue(context.Background(), queue.Task{Kind: "demo", Payload: []byte("p"), IdempotencyKey: "ev-1"}))
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, int32(1), runs.Load(), "a re-dispatch after completion must not run the task again")
}

func TestWorkerWithoutDoneMarkerDeletesDedupKeyOnAck(t *testing.T) {
	mr, enq, runs := runOneTask(t, 0)
	require.Eventually(t, func() bool { return !mr.Exists("dm:dedup:demo:ev-1") }, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, enq.Enqueue(context.Background(), queue.Task{Kind: "demo", Payload: []byte("p"), IdempotencyKey: "ev-1"}))
	require.Eventually(t, func() bool { return runs.Load() == 2 }, 2*time.Second, 5*time.Millisecond)
}
