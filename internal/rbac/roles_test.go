package rbac

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRolePermissionsMatrix(t *testing.T) {
	all := AllPermissions()
	require.Len(t, all, 19)
	for _, p := range all {
		assert.False(t, strings.HasPrefix(string(p), "integrations:"), "unexpected permission %s", p)
		assert.False(t, strings.HasPrefix(string(p), "audit:"), "unexpected permission %s", p)
		assert.True(t, RoleOwner.Has(p), "owner must have %s", p)
		assert.True(t, RoleAdmin.Has(p), "admin must have %s", p)
		assert.False(t, RoleCustomer.Has(p), "customer must not have %s", p)
	}
	assert.Empty(t, PermissionsFor(RoleCustomer))
	assert.NotNil(t, PermissionsFor(RoleCustomer))

	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleManager, PermCatalogWrite, true},
		{RoleManager, PermRefundsWrite, true},
		{RoleManager, PermSettingsRead, true},
		{RoleManager, PermStaffRead, true},
		{RoleManager, PermSettingsWrite, false},
		{RoleManager, PermStaffManage, false},
		{RoleCatalog, PermCatalogWrite, true},
		{RoleCatalog, PermInventoryWrite, true},
		{RoleCatalog, PermPromotionsRead, true},
		{RoleCatalog, PermPromotionsWrite, false},
		{RoleCatalog, PermOrdersWrite, false},
		{RoleFulfillment, PermOrdersWrite, true},
		{RoleFulfillment, PermReturnsWrite, true},
		{RoleFulfillment, PermCustomersRead, true},
		{RoleFulfillment, PermRefundsWrite, false},
		{RoleFulfillment, PermCatalogWrite, false},
		{RoleSupport, PermSupportWrite, true},
		{RoleSupport, PermReturnsWrite, true},
		{RoleSupport, PermRefundsWrite, false},
		{RoleSupport, PermOrdersWrite, false},
		{RoleAnalyst, PermAnalyticsRead, true},
		{RoleAnalyst, PermOrdersRead, true},
		{RoleAnalyst, PermOrdersWrite, false},
		{RoleAnalyst, PermStaffRead, false},
		{RoleOwner, "", false},
		{Role("bogus"), PermCatalogRead, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.role.Has(tc.perm), "%s has %s", tc.role, tc.perm)
	}
}

func TestParseRoleAndDBRoundTrip(t *testing.T) {
	for _, r := range append(AllRoles(), RoleCustomer) {
		parsed, ok := ParseRole(string(r))
		require.True(t, ok, string(r))
		assert.Equal(t, r, parsed)
		assert.Equal(t, r, RoleFromDB(r.DB()))
	}
	assert.Equal(t, "MANAGER", RoleManager.DB())
	assert.Equal(t, RoleCustomer, RoleFromDB("BOGUS"))
	assert.Equal(t, RoleCustomer, RoleFromDB("owner"), "DB values are uppercase")
	_, ok := ParseRole("Owner")
	assert.False(t, ok)
	_, ok = ParseRole("")
	assert.False(t, ok)
}

func TestStaffRoles(t *testing.T) {
	require.Equal(t, []Role{RoleOwner, RoleAdmin, RoleManager, RoleCatalog, RoleFulfillment, RoleSupport, RoleAnalyst}, AllRoles())
	for _, r := range AllRoles() {
		assert.True(t, r.IsStaff(), string(r))
		assert.NotEmpty(t, PermissionsFor(r), string(r))
	}
	assert.False(t, RoleCustomer.IsStaff())
	assert.False(t, Role("bogus").IsStaff())
	assert.Less(t, RoleOwner.Rank(), RoleAdmin.Rank())
	assert.Less(t, RoleAdmin.Rank(), RoleManager.Rank())
}

func TestPermissionsForReturnsCopy(t *testing.T) {
	perms := PermissionsFor(RoleCatalog)
	perms[0] = "mutated"
	assert.NotContains(t, PermissionsFor(RoleCatalog), Permission("mutated"))
	assert.IsIncreasing(t, PermissionStrings(RoleManager))
}
