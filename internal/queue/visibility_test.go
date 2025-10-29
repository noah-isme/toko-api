package queue_test

import (
	"context"
	"io"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/queue"
)

func TestVisibilityTimeoutRequeue(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	enq := queue.Enqueuer{R: client, Prefix: "vis", DedupTTL: time.Minute, MaxAttempts: 3}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	attempts := make(chan int, 2)
	log := zerolog.New(io.Discard)
	worker := queue.Worker{
		R:                 client,
		Prefix:            "vis",
		Kind:              "webhook",
		Concurrency:       1,
		VisibilityTimeout: 150 * time.Millisecond,
		SoftDeadline:      80 * time.Millisecond,
		RetryBase:         20 * time.Millisecond,
		RetryJitter:       0.0,
		Store:             newMemoryStore(),
		Logger:            &log,
		Handler: func(jobCtx context.Context, task queue.Task) error {
			attempts <- task.Attempt
			if task.Attempt == 1 {
				<-jobCtx.Done()
				return jobCtx.Err()
			}
			cancel()
			return nil
		},
	}

	done := make(chan struct{})
	go func() {
		_ = worker.Run(ctx)
		close(done)
	}()

	require.NoError(t, enq.Enqueue(context.Background(), queue.Task{Kind: "webhook", Payload: []byte("payload"), IdempotencyKey: "a1", MaxAttempts: 3}))

	require.Eventually(t, func() bool {
		return len(attempts) >= 2
	}, 2*time.Second, 20*time.Millisecond)

	first := <-attempts
	second := <-attempts
	require.Equal(t, 1, first)
	require.Equal(t, 2, second)

	<-done

	depth, err := client.ZCard(context.Background(), "vis:queue:webhook").Result()
	require.NoError(t, err)
	require.Equal(t, int64(0), depth)
}
