package notify

import (
	"context"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RedisReplayProtector implements ReplayProtector using Redis SETNX semantics.
type RedisReplayProtector struct {
	Client *redis.Client
}

// Acquire attempts to claim the delivery key for the provided TTL.
func (r RedisReplayProtector) Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if r.Client == nil {
		return true, nil
	}
	return r.Client.SetNX(ctx, key, "1", ttl).Result()
}

// Release removes the replay guard key.
func (r RedisReplayProtector) Release(ctx context.Context, key string) error {
	if r.Client == nil {
		return nil
	}
	return r.Client.Del(ctx, key).Err()
}
