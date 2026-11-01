// Package rbac implements tenant-scoped staff roles, the static role to
// permission matrix, and the HTTP guards that enforce it on admin routes.
//
// Authorization for tenant resources comes from tenant_memberships.role. The
// API exposes lowercase role slugs while the database keeps the existing
// uppercase values. Platform-global resources (webhooks, queue, audit logs,
// cross-tenant analytics) are outside the matrix and stay behind the global
// users.roles "admin" check (RequirePlatformAdmin).
package rbac

import (
	"slices"
	"strings"
)

// Role is a tenant staff role slug as exposed by the API.
type Role string

// Permission is a stable permission identifier.
type Permission string

// Roles. RoleCustomer is the non-staff role every storefront member has.
const (
	RoleOwner       Role = "owner"
	RoleAdmin       Role = "admin"
	RoleManager     Role = "manager"
	RoleCatalog     Role = "catalog"
	RoleFulfillment Role = "fulfillment"
	RoleSupport     Role = "support"
	RoleAnalyst     Role = "analyst"
	RoleCustomer    Role = "customer"
)

// Permissions. There is deliberately no permission for webhooks, the job
// queue or audit logs: those are platform-global and gated by
// RequirePlatformAdmin instead.
const (
	PermCatalogRead     Permission = "catalog:read"
	PermCatalogWrite    Permission = "catalog:write"
	PermInventoryRead   Permission = "inventory:read"
	PermInventoryWrite  Permission = "inventory:write"
	PermOrdersRead      Permission = "orders:read"
	PermOrdersWrite     Permission = "orders:write"
	PermReturnsRead     Permission = "returns:read"
	PermReturnsWrite    Permission = "returns:write"
	PermRefundsWrite    Permission = "refunds:write"
	PermCustomersRead   Permission = "customers:read"
	PermSupportRead     Permission = "support:read"
	PermSupportWrite    Permission = "support:write"
	PermPromotionsRead  Permission = "promotions:read"
	PermPromotionsWrite Permission = "promotions:write"
	PermAnalyticsRead   Permission = "analytics:read"
	PermSettingsRead    Permission = "settings:read"
	PermSettingsWrite   Permission = "settings:write"
	PermStaffRead       Permission = "staff:read"
	PermStaffManage     Permission = "staff:manage"
)

// AllPermissions returns every permission in the matrix, sorted.
func AllPermissions() []Permission {
	out := []Permission{
		PermCatalogRead, PermCatalogWrite,
		PermInventoryRead, PermInventoryWrite,
		PermOrdersRead, PermOrdersWrite,
		PermReturnsRead, PermReturnsWrite,
		PermRefundsWrite,
		PermCustomersRead,
		PermSupportRead, PermSupportWrite,
		PermPromotionsRead, PermPromotionsWrite,
		PermAnalyticsRead,
		PermSettingsRead, PermSettingsWrite,
		PermStaffRead, PermStaffManage,
	}
	slices.Sort(out)
	return out
}

func without(all []Permission, drop ...Permission) []Permission {
	out := make([]Permission, 0, len(all))
	for _, p := range all {
		if !slices.Contains(drop, p) {
			out = append(out, p)
		}
	}
	return out
}

// rolePermissions is the static product policy (plan Decision 3).
var rolePermissions = map[Role][]Permission{
	RoleOwner:   AllPermissions(),
	RoleAdmin:   AllPermissions(),
	RoleManager: without(AllPermissions(), PermSettingsWrite, PermStaffManage),
	RoleCatalog: {
		PermCatalogRead, PermCatalogWrite, PermInventoryRead, PermInventoryWrite, PermPromotionsRead,
	},
	RoleFulfillment: {
		PermOrdersRead, PermOrdersWrite, PermReturnsRead, PermReturnsWrite, PermInventoryRead, PermCustomersRead,
	},
	RoleSupport: {
		PermSupportRead, PermSupportWrite, PermOrdersRead, PermReturnsRead, PermReturnsWrite, PermCustomersRead,
	},
	RoleAnalyst: {
		PermAnalyticsRead, PermOrdersRead, PermCatalogRead, PermPromotionsRead, PermCustomersRead,
	},
	RoleCustomer: {},
}

// AllRoles returns the staff roles in rank order (owner first). The non-staff
// customer role is not included.
func AllRoles() []Role {
	return []Role{RoleOwner, RoleAdmin, RoleManager, RoleCatalog, RoleFulfillment, RoleSupport, RoleAnalyst}
}

// ParseRole parses a lowercase API role slug, including "customer".
// Matching is case-sensitive.
func ParseRole(s string) (Role, bool) {
	r := Role(s)
	if _, ok := rolePermissions[r]; ok {
		return r, true
	}
	return "", false
}

// RoleFromDB maps a tenant_memberships.role value to a Role. Unknown values
// map to customer so an unexpected value never grants access.
func RoleFromDB(s string) Role {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	if _, ok := rolePermissions[r]; ok && strings.ToUpper(string(r)) == s {
		return r
	}
	return RoleCustomer
}

// DB returns the tenant_memberships.role value for r.
func (r Role) DB() string {
	return strings.ToUpper(string(r))
}

// PermissionsFor returns a sorted copy of the permissions granted to r. The
// result is never nil (empty for customer and unknown roles).
func PermissionsFor(r Role) []Permission {
	perms := rolePermissions[r]
	out := make([]Permission, len(perms))
	copy(out, perms)
	slices.Sort(out)
	return out
}

// PermissionStrings returns PermissionsFor(r) as plain strings (never nil).
func PermissionStrings(r Role) []string {
	perms := PermissionsFor(r)
	out := make([]string, len(perms))
	for i, p := range perms {
		out[i] = string(p)
	}
	return out
}

// Has reports whether r grants p.
func (r Role) Has(p Permission) bool {
	return p != "" && slices.Contains(rolePermissions[r], p)
}

// IsStaff reports whether r is a staff role (anything but customer).
func (r Role) IsStaff() bool {
	_, ok := rolePermissions[r]
	return ok && r != RoleCustomer
}

// Rank orders roles for display: lower is more privileged.
func (r Role) Rank() int {
	for i, role := range AllRoles() {
		if role == r {
			return i
		}
	}
	return len(AllRoles())
}
