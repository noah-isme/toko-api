package lock_test

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/lock"
)

func TestWithLockIdempotent(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	locker := lock.Locker{R: client, RetryBackoff: 5 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var order []string
	var mu sync.Mutex
	firstDone := make(chan struct{})
	releaseFirst := make(chan struct{})

	go func() {
		err := locker.WithLock(ctx, "demo", 100*time.Millisecond, func(context.Context) error {
			mu.Lock()
			order = append(order, "first")
			mu.Unlock()
			close(firstDone)
			<-releaseFirst
			return nil
		})
		require.NoError(t, err)
	}()

	<-firstDone

	go func() {
		err := locker.WithLock(ctx, "demo", 100*time.Millisecond, func(context.Context) error {
			mu.Lock()
			order = append(order, "second")
			mu.Unlock()
			return nil
		})
		require.NoError(t, err)
	}()

	close(releaseFirst)
	time.Sleep(20 * time.Millisecond)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 2
	}, time.Second, 10*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"first", "second"}, order)
}

func TestTryLockReturnsFalseWhenHeld(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	locker := lock.Locker{R: client}
	ctx := context.Background()

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		acquired, err := locker.TryLock(ctx, "job", time.Minute, func(context.Context) error {
			close(held)
			<-release
			return nil
		})
		require.NoError(t, err)
		require.True(t, acquired)
	}()
	<-held

	ran := false
	acquired, err := locker.TryLock(ctx, "job", time.Minute, func(context.Context) error {
		ran = true
		return nil
	})
	require.NoError(t, err)
	require.False(t, acquired)
	require.False(t, ran)

	close(release)
	<-done
	acquired, err = locker.TryLock(ctx, "job", time.Minute, func(context.Context) error {
		ran = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, acquired)
	require.True(t, ran)
	require.False(t, mr.Exists("job"), "lock must be released after fn returns")
}
