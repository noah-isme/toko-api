package rbac

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// RoleStore loads the caller's ACTIVE membership role. *dbgen.Queries
// satisfies it.
type RoleStore interface {
	GetTenantMembershipRole(ctx context.Context, arg dbgen.GetTenantMembershipRoleParams) (string, error)
}

// UserStore loads a user's global roles. *dbgen.Queries satisfies it.
type UserStore interface {
	GetUserByID(ctx context.Context, id pgtype.UUID) (dbgen.GetUserByIDRow, error)
}

// Details values of a 403 FORBIDDEN response that are not permissions.
const (
	RequiredStaff         = "staff"
	RequiredPlatformAdmin = "platform-admin"
)

func parsePGUUID(raw string) (pgtype.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func forbidden(w http.ResponseWriter, message, required string) {
	common.JSONError(w, http.StatusForbidden, "FORBIDDEN", message, map[string]string{"required": required})
}

// RequireMembership prevents an authenticated user from selecting an
// arbitrary tenant with X-Tenant-ID: every authenticated workflow requires an
// ACTIVE membership row. On success the caller's role in the tenant is stored
// on the request context (RoleFromContext).
func RequireMembership(store RoleStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store == nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "tenant membership store not configured", nil)
				return
			}
			userID, hasUser := common.UserID(r.Context())
			tenantID, hasTenant := tenant.FromContext(r.Context())
			if !hasUser || !hasTenant {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication and tenant are required", nil)
				return
			}
			uid, userErr := parsePGUUID(userID)
			tid, tenantErr := parsePGUUID(tenantID)
			if userErr != nil || tenantErr != nil {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication or tenant context", nil)
				return
			}
			role, err := store.GetTenantMembershipRole(r.Context(), dbgen.GetTenantMembershipRoleParams{TenantID: tid, UserID: uid})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					common.JSONError(w, http.StatusForbidden, "TENANT_FORBIDDEN", "user is not a member of this tenant", nil)
					return
				}
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to validate tenant membership", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithRole(r.Context(), RoleFromDB(role))))
		})
	}
}

// RequireStaff is the deny-by-default group guard for /admin and /analytics:
// members whose tenant role is customer never reach any handler, even one
// that was registered without a permission.
func RequireStaff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := RoleFromContext(r.Context())
		if !ok || !role.IsStaff() {
			forbidden(w, "staff role required", RequiredStaff)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePlatformAdmin admits only users whose global users.roles contains
// "admin". It guards platform-global resources that no tenant role grants.
func RequirePlatformAdmin(store UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if store == nil {
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "role validator not configured", nil)
				return
			}
			userID, ok := common.UserID(r.Context())
			if !ok {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
				return
			}
			uid, err := parsePGUUID(userID)
			if err != nil {
				common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication context", nil)
				return
			}
			user, err := store.GetUserByID(r.Context(), uid)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					forbidden(w, "insufficient permissions", RequiredPlatformAdmin)
					return
				}
				common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load user roles", nil)
				return
			}
			if !slices.Contains(user.Roles, "admin") {
				forbidden(w, "insufficient permissions", RequiredPlatformAdmin)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPlatformAdmin(r.Context())))
		})
	}
}

// Guarded is an endpoint handler that enforces one permission (or the
// platform-admin flag) before calling Next. Every /admin and /analytics route
// is registered as a Guarded value so a router walk can prove none is
// unguarded. Register it with chi's Method/Handle (they take an http.Handler),
// e.g. admin.Method(http.MethodGet, "/products", rbac.Require(...)).
type Guarded struct {
	Permission Permission
	Platform   bool
	Next       http.Handler
}

// ServeHTTP implements http.Handler.
func (g Guarded) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.Platform {
		// Defense in depth: a platform route accidentally registered outside
		// the RequirePlatformAdmin group fails closed.
		if !IsPlatformAdmin(r.Context()) {
			forbidden(w, "insufficient permissions", RequiredPlatformAdmin)
			return
		}
	} else {
		role, ok := RoleFromContext(r.Context())
		if !ok || !role.Has(g.Permission) {
			forbidden(w, "insufficient permissions", string(g.Permission))
			return
		}
	}
	if g.Next == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "handler not configured", nil)
		return
	}
	g.Next.ServeHTTP(w, r)
}

// Require guards h with permission p. It panics on an empty permission so a
// misconfigured route fails at startup.
func Require(p Permission, h http.HandlerFunc) Guarded {
	if p == "" {
		panic("rbac: Require called with an empty permission")
	}
	return Guarded{Permission: p, Next: handlerOrNil(h)}
}

func handlerOrNil(h http.HandlerFunc) http.Handler {
	if h == nil {
		return nil
	}
	return h
}

// RequirePlatform guards h with the platform-admin flag set by
// RequirePlatformAdmin.
func RequirePlatform(h http.HandlerFunc) Guarded {
	return Guarded{Platform: true, Next: handlerOrNil(h)}
}
