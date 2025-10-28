package common

import "context"

type ctxKey string

const userIDKey ctxKey = "auth/user-id"

// WithUserID stores the authenticated user identifier on the provided context.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserID extracts the authenticated user identifier from the context if present.
func UserID(ctx context.Context) (string, bool) {
	v := ctx.Value(userIDKey)
	if v == nil {
		return "", false
	}
	id, ok := v.(string)
	return id, ok
}
