package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache wraps Redis helpers for JSON payloads.
type Cache struct {
	client *redis.Client
	ttl    time.Duration
}

// NewCache constructs a cache helper.
func NewCache(client *redis.Client, ttl time.Duration) *Cache {
	if client == nil || ttl <= 0 {
		return &Cache{client: client, ttl: ttl}
	}
	return &Cache{client: client, ttl: ttl}
}

// GetJSON unmarshals a cached JSON payload into dst. It reports whether the key existed.
func (c *Cache) GetJSON(ctx context.Context, key string, dst any) (bool, error) {
	if c == nil || c.client == nil || key == "" {
		return false, nil
	}
	data, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return false, err
	}
	return true, nil
}

// SetJSON serialises v as JSON and stores it with the configured TTL.
func (c *Cache) SetJSON(ctx context.Context, key string, v any) error {
	if c == nil || c.client == nil || key == "" {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, data, c.ttl).Err()
}
