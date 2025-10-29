package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache wraps Redis helpers for JSON payloads and exposes convenience invalidation helpers.
type Cache struct {
	client *redis.Client
	ttl    time.Duration
	prefix string
}

// NewCache constructs a cache helper with an optional namespace prefix.
func NewCache(client *redis.Client, ttl time.Duration, prefix string) *Cache {
	clean := strings.Trim(prefix, ": ")
	return &Cache{client: client, ttl: ttl, prefix: clean}
}

func (c *Cache) key(parts ...string) string {
	sanitized := make([]string, 0, len(parts)+1)
	if c != nil && c.prefix != "" {
		sanitized = append(sanitized, c.prefix)
	}
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			sanitized = append(sanitized, trimmed)
		}
	}
	if len(sanitized) == 0 {
		return ""
	}
	return strings.Join(sanitized, ":")
}

// ProductListKey returns the cache key for the default product listing payload.
func (c *Cache) ProductListKey() string {
	return c.key("catalog", "products", "list", "popular")
}

// ProductDetailKey returns the cache key for a product detail payload.
func (c *Cache) ProductDetailKey(slug string) string {
	return c.key("catalog", "products", "detail", slug)
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

// Delete removes the provided keys from the cache.
func (c *Cache) Delete(ctx context.Context, keys ...string) {
	if c == nil || c.client == nil {
		return
	}
	filtered := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			filtered = append(filtered, key)
		}
	}
	if len(filtered) == 0 {
		return
	}
	_ = c.client.Del(ctx, filtered...).Err()
}

// InvalidateProduct removes cached product detail and list entries impacted by the slug.
func (c *Cache) InvalidateProduct(ctx context.Context, slug string) {
	if c == nil {
		return
	}
	c.Delete(ctx, c.ProductDetailKey(slug))
	c.InvalidateList(ctx)
}

// InvalidateList removes the cached list payload.
func (c *Cache) InvalidateList(ctx context.Context) {
	if c == nil {
		return
	}
	c.Delete(ctx, c.ProductListKey())
}
