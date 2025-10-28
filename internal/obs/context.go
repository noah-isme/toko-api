package obs

import "context"

// routePatternKey is the context key storing matched route pattern.
type routePatternKey struct{}

// WithRoutePattern stores the matched router pattern on the context.
func WithRoutePattern(ctx context.Context, pattern string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, routePatternKey{}, pattern)
}

// RoutePatternFromContext extracts the route pattern from context if present.
func RoutePatternFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(routePatternKey{}).(string); ok {
		return v
	}
	return ""
}
