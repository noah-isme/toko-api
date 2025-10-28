package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Limiter implements a sliding window rate limiter backed by Redis sorted sets.
type Limiter struct {
	Client *redis.Client
	Prefix string
}

// Allow registers an event for the given key and returns whether it is within the limit.
func (l Limiter) Allow(ctx context.Context, key string, window time.Duration, max int) (allowed bool, remaining int, reset time.Time, err error) {
	if l.Client == nil || max <= 0 || window <= 0 {
		return true, max, time.Now().Add(window), nil
	}

	now := time.Now()
	until := now.Add(window)
	score := float64(now.UnixNano())
	cutoff := float64(now.Add(-window).UnixNano())

	redisKey := l.Prefix + key
	member := fmt.Sprintf("%s:%s", key, uuid.NewString())

	pipe := l.Client.TxPipeline()
	pipe.ZRemRangeByScore(ctx, redisKey, "-inf", fmt.Sprintf("%f", cutoff))
	pipe.ZAdd(ctx, redisKey, redis.Z{Score: score, Member: member})
	countCmd := pipe.ZCard(ctx, redisKey)
	pipe.Expire(ctx, redisKey, window)
	if _, err = pipe.Exec(ctx); err != nil {
		return false, 0, until, err
	}

	current := int(countCmd.Val())
	remaining = max - current
	if remaining < 0 {
		remaining = 0
	}
	allowed = current <= max
	return allowed, remaining, until, nil
}
