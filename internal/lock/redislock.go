package lock

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Locker provides a Redis-backed distributed lock.
type Locker struct {
	R            *redis.Client
	RetryBackoff time.Duration
}

// WithLock executes fn while holding a lock for the provided key. The lock is
// released automatically even if fn returns an error. When the lock cannot be
// acquired before the context is cancelled an error is returned.
func (l Locker) WithLock(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) error) error {
	if l.R == nil {
		return errors.New("lock: redis client not configured")
	}
	if fn == nil {
		return errors.New("lock: callback not provided")
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	token := uuid.NewString()
	retry := l.RetryBackoff
	if retry <= 0 {
		retry = 50 * time.Millisecond
	}

	for {
		ok, err := l.R.SetNX(ctx, key, token, ttl).Result()
		if err != nil {
			return err
		}
		if ok {
			defer l.release(context.Background(), key, token)
			return fn(ctx)
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l Locker) release(ctx context.Context, key, token string) {
	const script = `if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
else
  return 0
end`
	if err := l.R.Eval(ctx, script, []string{key}, token).Err(); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unknown command") {
			_ = l.R.Del(ctx, key).Err()
		}
	}
}
