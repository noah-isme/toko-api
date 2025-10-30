package tenant

import "context"

// With stores tenant identifier into the provided context.
func With(ctx context.Context, id string) context.Context {
	return WithTenant(ctx, id)
}
