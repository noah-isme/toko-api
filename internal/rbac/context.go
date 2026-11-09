package rbac

import "context"

type roleKey struct{}

type platformAdminKey struct{}

// WithRole stores the caller's role in the resolved tenant.
func WithRole(ctx context.Context, r Role) context.Context {
	return context.WithValue(ctx, roleKey{}, r)
}

// RoleFromContext returns the caller's tenant role set by RequireMembership.
func RoleFromContext(ctx context.Context) (Role, bool) {
	r, ok := ctx.Value(roleKey{}).(Role)
	return r, ok && r != ""
}

// WithPlatformAdmin marks the request as coming from a global platform admin.
func WithPlatformAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, platformAdminKey{}, true)
}

// IsPlatformAdmin reports whether RequirePlatformAdmin admitted the request.
func IsPlatformAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(platformAdminKey{}).(bool)
	return v
}
