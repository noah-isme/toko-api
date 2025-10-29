package queue_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/queue"
)

func TestEnqueueDequeue(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	enq := queue.Enqueuer{R: client, Prefix: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = enq.Enqueue(ctx, queue.Task{Kind: "demo", Payload: []byte("payload"), IdempotencyKey: "1"})
	require.NoError(t, err)

	processed := make(chan []byte, 1)
	worker := queue.Worker{
		R:                 client,
		Prefix:            "test",
		Kind:              "demo",
		Concurrency:       1,
		VisibilityTimeout: time.Second,
		RetryBase:         10 * time.Millisecond,
		Handler: func(ctx context.Context, task queue.Task) error {
			processed <- task.Payload
			cancel()
			return nil
		},
	}

	go func() {
		_ = worker.Run(ctx)
	}()

	select {
	case payload := <-processed:
		require.Equal(t, []byte("payload"), payload)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for payload")
	}
}

func TestWorkerRetries(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	enq := queue.Enqueuer{R: client, Prefix: "retry"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, enq.Enqueue(ctx, queue.Task{Kind: "demo", Payload: []byte("retry"), IdempotencyKey: "r1", MaxAttempts: 3}))

	var attempts atomic.Int32
	worker := queue.Worker{
		R:                 client,
		Prefix:            "retry",
		Kind:              "demo",
		Concurrency:       1,
		VisibilityTimeout: time.Second,
		RetryBase:         5 * time.Millisecond,
		RetryJitter:       0.1,
		Handler: func(ctx context.Context, task queue.Task) error {
			if attempts.Add(1) == 1 {
				return errors.New("fail first")
			}
			cancel()
			return nil
		},
	}

	go func() { _ = worker.Run(ctx) }()

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not retry in time")
	}

	require.GreaterOrEqual(t, attempts.Load(), int32(2))
}
