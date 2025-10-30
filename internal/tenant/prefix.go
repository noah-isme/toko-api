package tenant

import "context"

// From exposes the tenant identifier retrieval helper.
func From(ctx context.Context) (string, bool) {
	return FromContext(ctx)
}

// PrefixKey creates a namespaced cache/queue key per tenant slug or id.
func PrefixKey(tenantSlugOrID, key string) string {
	if tenantSlugOrID == "" {
		return key
	}
	return tenantSlugOrID + ":" + key
}
