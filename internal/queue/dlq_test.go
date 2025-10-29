package queue_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/queue"
)

func TestMoveToDLQAfterMaxAttempts(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store := newMemoryStore()
	enq := queue.Enqueuer{R: client, Prefix: "dlq", MaxAttempts: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log := zerolog.New(io.Discard)
	worker := queue.Worker{
		R:                 client,
		Prefix:            "dlq",
		Kind:              "webhook",
		Concurrency:       1,
		VisibilityTimeout: 120 * time.Millisecond,
		RetryBase:         20 * time.Millisecond,
		Store:             store,
		Logger:            &log,
		Handler: func(context.Context, queue.Task) error {
			return errors.New("fail")
		},
	}

	done := make(chan struct{})
	go func() {
		_ = worker.Run(ctx)
		close(done)
	}()

	require.NoError(t, enq.Enqueue(context.Background(), queue.Task{Kind: "webhook", Payload: []byte("body"), IdempotencyKey: "dlq1", MaxAttempts: 2}))

	require.Eventually(t, func() bool {
		count, err := store.CountQueueDlq(context.Background(), "webhook")
		return err == nil && count == 1
	}, 2*time.Second, 20*time.Millisecond)

	snapshot := store.snapshot()
	require.Len(t, snapshot, 1)
	for _, entry := range snapshot {
		require.Equal(t, "webhook", entry.Kind)
		require.Equal(t, "dlq1", entry.IdempotencyKey)
		require.Equal(t, 2, entry.Attempts)
		require.NotEmpty(t, entry.Payload)
	}

	cancel()
	<-done
}
