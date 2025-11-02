package cache

import (
	"context"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

// KeyCatalogList returns a per-tenant cache key for catalog lists.
func KeyCatalogList(ctx context.Context, base string) string {
	id, ok := tenant.From(ctx)
	if !ok {
		return base
	}
	return id + ":" + base
}

// KeyProduct returns a per-tenant key for a given product slug.
func KeyProduct(ctx context.Context, slug string) string {
	id, ok := tenant.From(ctx)
	if !ok {
		return "product:" + slug
	}
	return id + ":product:" + slug
}
