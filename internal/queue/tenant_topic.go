package queue

import (
	"context"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Topic returns a per-tenant queue topic, e.g. "<tenant>:webhook-delivery".
func Topic(ctx context.Context, kind string) string {
	if t, ok := tenant.From(ctx); ok {
		return t + ":" + kind
	}
	return kind
}
