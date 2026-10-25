package rbac

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/audit"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type fakeUser struct {
	name, email string
}

// fakeStaffStore is an in-memory, single-tenant StaffStore. Any query for a
// different tenant behaves like an empty table.
type fakeStaffStore struct {
	tenantID    pgtype.UUID
	users       map[pgtype.UUID]fakeUser
	memberships map[pgtype.UUID]dbgen.TenantMembership
	// ownerCount, when >= 0, overrides the computed ACTIVE owner count.
	ownerCount int64
	calls      []string
	lockTenant pgtype.UUID
	updates    []dbgen.UpdateTenantMembershipRoleParams
	activates  []dbgen.ActivateTenantMembershipRoleParams
}

func newFakeStaffStore(tenantID pgtype.UUID) *fakeStaffStore {
	return &fakeStaffStore{
		tenantID:    tenantID,
		users:       map[pgtype.UUID]fakeUser{},
		memberships: map[pgtype.UUID]dbgen.TenantMembership{},
		ownerCount:  -1,
	}
}

func (f *fakeStaffStore) add(role, status, email string) pgtype.UUID {
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	f.users[id] = fakeUser{name: strings.Split(email, "@")[0], email: email}
	f.memberships[id] = dbgen.TenantMembership{TenantID: f.tenantID, UserID: id, Role: role, Status: status}
	return id
}

func (f *fakeStaffStore) ListTenantStaff(_ context.Context, arg dbgen.ListTenantStaffParams) ([]dbgen.ListTenantStaffRow, error) {
	f.calls = append(f.calls, "ListTenantStaff")
	var rows []dbgen.ListTenantStaffRow
	if arg.TenantID != f.tenantID {
		return rows, nil
	}
	for id, m := range f.memberships {
		if m.Role == "CUSTOMER" {
			continue
		}
		u := f.users[id]
		rows = append(rows, dbgen.ListTenantStaffRow{UserID: id, Name: u.name, Email: u.email, Role: m.Role, Status: m.Status})
	}
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := RoleFromDB(rows[i].Role).Rank(), RoleFromDB(rows[j].Role).Rank()
		if ri != rj {
			return ri < rj
		}
		return rows[i].Email < rows[j].Email
	})
	start := int(arg.PageOffset)
	if start > len(rows) {
		start = len(rows)
	}
	end := start + int(arg.PageLimit)
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], nil
}

func (f *fakeStaffStore) CountTenantStaff(_ context.Context, tenantID pgtype.UUID) (int64, error) {
	f.calls = append(f.calls, "CountTenantStaff")
	if tenantID != f.tenantID {
		return 0, nil
	}
	var n int64
	for _, m := range f.memberships {
		if m.Role != "CUSTOMER" {
			n++
		}
	}
	return n, nil
}

func (f *fakeStaffStore) GetTenantStaffMember(_ context.Context, arg dbgen.GetTenantStaffMemberParams) (dbgen.GetTenantStaffMemberRow, error) {
	f.calls = append(f.calls, "GetTenantStaffMember")
	m, ok := f.memberships[arg.UserID]
	if !ok || arg.TenantID != f.tenantID {
		return dbgen.GetTenantStaffMemberRow{}, pgx.ErrNoRows
	}
	u := f.users[arg.UserID]
	return dbgen.GetTenantStaffMemberRow{UserID: m.UserID, Name: u.name, Email: u.email, Role: m.Role, Status: m.Status}, nil
}

func (f *fakeStaffStore) GetTenantMemberUserIDByEmail(_ context.Context, arg dbgen.GetTenantMemberUserIDByEmailParams) (pgtype.UUID, error) {
	f.calls = append(f.calls, "GetTenantMemberUserIDByEmail")
	if arg.TenantID != f.tenantID {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	for id, u := range f.users {
		if strings.EqualFold(u.email, arg.Email) {
			if _, member := f.memberships[id]; member {
				return id, nil
			}
		}
	}
	return pgtype.UUID{}, pgx.ErrNoRows
}

func (f *fakeStaffStore) GetTenantMembershipForUpdate(_ context.Context, arg dbgen.GetTenantMembershipForUpdateParams) (dbgen.TenantMembership, error) {
	f.calls = append(f.calls, "GetTenantMembershipForUpdate")
	m, ok := f.memberships[arg.UserID]
	if !ok || arg.TenantID != f.tenantID {
		return dbgen.TenantMembership{}, pgx.ErrNoRows
	}
	return m, nil
}

func (f *fakeStaffStore) CountTenantOwners(_ context.Context, tenantID pgtype.UUID) (int64, error) {
	f.calls = append(f.calls, "CountTenantOwners")
	if f.ownerCount >= 0 {
		return f.ownerCount, nil
	}
	var n int64
	for _, m := range f.memberships {
		if m.Role == "OWNER" && m.Status == "ACTIVE" && tenantID == f.tenantID {
			n++
		}
	}
	return n, nil
}

func (f *fakeStaffStore) UpdateTenantMembershipRole(_ context.Context, arg dbgen.UpdateTenantMembershipRoleParams) (dbgen.TenantMembership, error) {
	f.calls = append(f.calls, "UpdateTenantMembershipRole")
	f.updates = append(f.updates, arg)
	m, ok := f.memberships[arg.UserID]
	if !ok || arg.TenantID != f.tenantID {
		return dbgen.TenantMembership{}, pgx.ErrNoRows
	}
	m.Role = arg.Role
	f.memberships[arg.UserID] = m
	return m, nil
}

func (f *fakeStaffStore) ActivateTenantMembershipRole(_ context.Context, arg dbgen.ActivateTenantMembershipRoleParams) (dbgen.TenantMembership, error) {
	f.calls = append(f.calls, "ActivateTenantMembershipRole")
	f.activates = append(f.activates, arg)
	m, ok := f.memberships[arg.UserID]
	if !ok || arg.TenantID != f.tenantID {
		return dbgen.TenantMembership{}, pgx.ErrNoRows
	}
	m.Role = arg.Role
	m.Status = "ACTIVE"
	f.memberships[arg.UserID] = m
	return m, nil
}

func (f *fakeStaffStore) LockTenantStaff(_ context.Context, tenantID pgtype.UUID) error {
	f.calls = append(f.calls, "LockTenantStaff")
	f.lockTenant = tenantID
	return nil
}

type staffFixture struct {
	t        *testing.T
	store    *fakeStaffStore
	tenantID pgtype.UUID
	router   http.Handler
	txCalls  int
}

func newStaffFixture(t *testing.T) *staffFixture {
	t.Helper()
	tenantID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	f := &staffFixture{t: t, store: newFakeStaffStore(tenantID), tenantID: tenantID}
	h := &StaffHandler{
		Q: f.store,
		RunInTx: func(_ context.Context, fn func(StaffStore) error) error {
			f.txCalls++
			f.store.calls = append(f.store.calls, "BEGIN")
			return fn(f.store)
		},
	}
	r := chi.NewRouter()
	r.Get("/admin/roles", h.Roles)
	r.Get("/admin/staff", h.List)
	r.Post("/admin/staff", h.Assign)
	r.Patch("/admin/staff/{userId}", h.ChangeRole)
	r.Delete("/admin/staff/{userId}", h.Revoke)
	f.router = r
	return f
}

// do sends a request as actor with ctxRole as the (possibly stale) role
// RequireMembership put on the context.
func (f *staffFixture) do(method, path string, actor pgtype.UUID, ctxRole Role, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := common.WithUserID(req.Context(), uuidString(actor))
	ctx = tenant.WithTenant(ctx, uuidString(f.tenantID))
	ctx = WithRole(ctx, ctxRole)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decodeError(t, rec).Error.Code
}

func staffData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body.Data
}

func TestAssignUnknownEmail404(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	rec := f.do(http.MethodPost, "/admin/staff", owner, RoleOwner, `{"email":"ghost@a.test","role":"manager"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, "USER_NOT_FOUND", errCode(t, rec))
}

func TestAssignNonMemberOfTenant404(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	// A platform user with no membership in this tenant (e.g. registered only
	// in another tenant) must be indistinguishable from an unknown email, and
	// must not be enrolled.
	outsider := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	f.store.users[outsider] = fakeUser{name: "Outsider", email: "outsider@b.test"}
	rec := f.do(http.MethodPost, "/admin/staff", owner, RoleOwner, `{"email":"outsider@b.test","role":"manager"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, "USER_NOT_FOUND", errCode(t, rec))
	assert.NotContains(t, rec.Body.String(), "Outsider")
	assert.Empty(t, f.store.activates)
	_, enrolled := f.store.memberships[outsider]
	assert.False(t, enrolled)
}

func TestAssignRejectsCustomerRole400(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	for _, body := range []string{
		`{"email":"x@a.test","role":"customer"}`,
		`{"email":"x@a.test","role":"bogus"}`,
		`{"email":"x@a.test","role":"Manager"}`,
		`{"email":"","role":"manager"}`,
		`not json`,
	} {
		rec := f.do(http.MethodPost, "/admin/staff", owner, RoleOwner, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.Equal(t, "VALIDATION_ERROR", errCode(t, rec), body)
	}
	assert.Zero(t, f.txCalls)
}

func TestAssignExistingStaff409(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	f.store.add("SUPPORT", "ACTIVE", "sup@a.test")
	rec := f.do(http.MethodPost, "/admin/staff", owner, RoleOwner, `{"email":"sup@a.test","role":"manager"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "STAFF_EXISTS", errCode(t, rec))
}

func TestAssignOwnerByAdmin403(t *testing.T) {
	f := newStaffFixture(t)
	admin := f.store.add("ADMIN", "ACTIVE", "admin@a.test")
	f.store.add("CUSTOMER", "ACTIVE", "c@a.test")
	rec := f.do(http.MethodPost, "/admin/staff", admin, RoleAdmin, `{"email":"c@a.test","role":"owner"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "OWNER_REQUIRED", errCode(t, rec))
	assert.Empty(t, f.store.activates)
}

func TestAssignCreatesMembership201(t *testing.T) {
	f := newStaffFixture(t)
	admin := f.store.add("ADMIN", "ACTIVE", "admin@a.test")
	cust := f.store.add("CUSTOMER", "SUSPENDED", "jane@a.test")
	rec := f.do(http.MethodPost, "/admin/staff", admin, RoleAdmin, `{"email":"Jane@A.test","role":"manager"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, f.store.activates, 1)
	assert.Equal(t, "MANAGER", f.store.activates[0].Role)
	assert.Equal(t, cust, f.store.activates[0].UserID)
	assert.Equal(t, f.tenantID, f.store.activates[0].TenantID)
	data := staffData(t, rec)
	assert.Equal(t, "manager", data["role"])
	assert.Equal(t, "ACTIVE", data["status"])
	assert.Equal(t, uuidString(cust), data["userId"])
	assert.Equal(t, "jane@a.test", data["email"])
}

func TestChangeRoleLastOwner409(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(owner), owner, RoleOwner, `{"role":"manager"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "LAST_OWNER", errCode(t, rec))
	assert.Equal(t, "OWNER", f.store.memberships[owner].Role)
	assert.Empty(t, f.store.updates)
}

func TestChangeRoleSuspendedOwnerNotProtected(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	suspended := f.store.add("OWNER", "SUSPENDED", "old@a.test")
	f.store.ownerCount = 1
	rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(suspended), owner, RoleOwner, `{"role":"manager"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "MANAGER", f.store.memberships[suspended].Role)
}

func TestChangeRoleLastOwnerUsesActiveCountOnly(t *testing.T) {
	for _, tc := range []struct {
		count int64
		want  int
	}{{1, http.StatusConflict}, {2, http.StatusOK}} {
		f := newStaffFixture(t)
		owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
		target := f.store.add("OWNER", "ACTIVE", "other@a.test")
		f.store.ownerCount = tc.count
		rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(target), owner, RoleOwner, `{"role":"admin"}`)
		assert.Equal(t, tc.want, rec.Code, "count=%d body=%s", tc.count, rec.Body.String())
	}
}

func TestChangeRoleOwnerTargetByAdmin403(t *testing.T) {
	f := newStaffFixture(t)
	f.store.add("OWNER", "ACTIVE", "owner@a.test")
	owner2 := f.store.add("OWNER", "ACTIVE", "owner2@a.test")
	admin := f.store.add("ADMIN", "ACTIVE", "admin@a.test")
	sup := f.store.add("SUPPORT", "ACTIVE", "sup@a.test")

	rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(owner2), admin, RoleAdmin, `{"role":"manager"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "OWNER_REQUIRED", errCode(t, rec))

	rec = f.do(http.MethodPatch, "/admin/staff/"+uuidString(sup), admin, RoleAdmin, `{"role":"owner"}`)
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "OWNER_REQUIRED", errCode(t, rec))

	rec = f.do(http.MethodDelete, "/admin/staff/"+uuidString(owner2), admin, RoleAdmin, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "OWNER_REQUIRED", errCode(t, rec))
	assert.Empty(t, f.store.updates)
}

func TestChangeRoleSuccess200(t *testing.T) {
	f := newStaffFixture(t)
	admin := f.store.add("ADMIN", "ACTIVE", "admin@a.test")
	sup := f.store.add("SUPPORT", "ACTIVE", "sup@a.test")
	rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(sup), admin, RoleAdmin, `{"role":"fulfillment"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "fulfillment", staffData(t, rec)["role"])
	assert.Equal(t, "FULFILLMENT", f.store.memberships[sup].Role)
}

func TestChangeRoleNotStaff404(t *testing.T) {
	f := newStaffFixture(t)
	admin := f.store.add("ADMIN", "ACTIVE", "admin@a.test")
	cust := f.store.add("CUSTOMER", "ACTIVE", "c@a.test")
	rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(cust), admin, RoleAdmin, `{"role":"support"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "STAFF_NOT_FOUND", errCode(t, rec))

	rec = f.do(http.MethodPatch, "/admin/staff/"+uuid.NewString(), admin, RoleAdmin, `{"role":"support"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "STAFF_NOT_FOUND", errCode(t, rec))

	rec = f.do(http.MethodPatch, "/admin/staff/not-a-uuid", admin, RoleAdmin, `{"role":"support"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "VALIDATION_ERROR", errCode(t, rec))

	rec = f.do(http.MethodPatch, "/admin/staff/"+uuidString(cust), admin, RoleAdmin, `{"role":"customer"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRevokeLastOwner409(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	rec := f.do(http.MethodDelete, "/admin/staff/"+uuidString(owner), owner, RoleOwner, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "LAST_OWNER", errCode(t, rec))
}

func TestRevokeSuccess204SetsCustomer(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	cat := f.store.add("CATALOG", "ACTIVE", "cat@a.test")
	rec := f.do(http.MethodDelete, "/admin/staff/"+uuidString(cat), owner, RoleOwner, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, "CUSTOMER", f.store.memberships[cat].Role)
	assert.Equal(t, "ACTIVE", f.store.memberships[cat].Status, "membership is kept")
}

func TestMutationsTakeTenantLockFirst(t *testing.T) {
	type call struct {
		method, path, body string
		target             func(f *staffFixture) string
	}
	for name, c := range map[string]call{
		"assign": {http.MethodPost, "/admin/staff", `{"email":"c@a.test","role":"support"}`, nil},
		"change": {http.MethodPatch, "/admin/staff/", `{"role":"analyst"}`, func(f *staffFixture) string { return f.store.add("SUPPORT", "ACTIVE", "s@a.test").String() }},
		"revoke": {http.MethodDelete, "/admin/staff/", "", func(f *staffFixture) string { return f.store.add("SUPPORT", "ACTIVE", "s@a.test").String() }},
	} {
		t.Run(name, func(t *testing.T) {
			f := newStaffFixture(t)
			owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
			f.store.add("CUSTOMER", "ACTIVE", "c@a.test")
			path := c.path
			if c.target != nil {
				path += c.target(f)
			}
			f.store.calls = nil
			rec := f.do(c.method, path, owner, RoleOwner, c.body)
			require.Less(t, rec.Code, 300, rec.Body.String())
			require.GreaterOrEqual(t, len(f.store.calls), 3)
			assert.Equal(t, []string{"BEGIN", "LockTenantStaff", "GetTenantMembershipForUpdate"}, f.store.calls[:3])
			assert.Equal(t, f.tenantID, f.store.lockTenant)
		})
	}
}

// TestMutationsUseInTransactionActorRole covers the time-of-check/time-of-use
// race: the context still says owner (read before the lock), but by the time
// the lock is held the actor was demoted. Every decision must use the fresh
// role.
func TestMutationsUseInTransactionActorRole(t *testing.T) {
	t.Run("demoted to manager loses staff:manage", func(t *testing.T) {
		f := newStaffFixture(t)
		f.store.add("OWNER", "ACTIVE", "owner@a.test")
		actor := f.store.add("MANAGER", "ACTIVE", "was-owner@a.test")
		f.store.add("CUSTOMER", "ACTIVE", "z@a.test")
		rec := f.do(http.MethodPost, "/admin/staff", actor, RoleOwner, `{"email":"z@a.test","role":"owner"}`)
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		body := decodeError(t, rec)
		assert.Equal(t, "FORBIDDEN", body.Error.Code)
		assert.Equal(t, "staff:manage", body.Error.Details["required"])
		assert.Empty(t, f.store.activates)
	})
	t.Run("demoted to admin cannot grant owner", func(t *testing.T) {
		f := newStaffFixture(t)
		f.store.add("OWNER", "ACTIVE", "owner@a.test")
		actor := f.store.add("ADMIN", "ACTIVE", "was-owner@a.test")
		f.store.add("CUSTOMER", "ACTIVE", "z@a.test")
		rec := f.do(http.MethodPost, "/admin/staff", actor, RoleOwner, `{"email":"z@a.test","role":"owner"}`)
		require.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, "OWNER_REQUIRED", errCode(t, rec))
	})
	t.Run("demoted actor cannot change roles", func(t *testing.T) {
		f := newStaffFixture(t)
		owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
		actor := f.store.add("MANAGER", "ACTIVE", "was-owner@a.test")
		rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(owner), actor, RoleOwner, `{"role":"manager"}`)
		require.Equal(t, http.StatusForbidden, rec.Code)
		rec = f.do(http.MethodDelete, "/admin/staff/"+uuidString(owner), actor, RoleOwner, "")
		require.Equal(t, http.StatusForbidden, rec.Code)
		assert.Empty(t, f.store.updates)
	})
	t.Run("suspended actor", func(t *testing.T) {
		f := newStaffFixture(t)
		actor := f.store.add("OWNER", "SUSPENDED", "owner@a.test")
		f.store.add("CUSTOMER", "ACTIVE", "z@a.test")
		rec := f.do(http.MethodPost, "/admin/staff", actor, RoleOwner, `{"email":"z@a.test","role":"support"}`)
		require.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, "TENANT_FORBIDDEN", errCode(t, rec))
	})
}

func TestListStaffPaginationEnvelope(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	f.store.add("SUPPORT", "ACTIVE", "b@a.test")
	f.store.add("ADMIN", "ACTIVE", "a@a.test")
	f.store.add("CUSTOMER", "ACTIVE", "c@a.test")
	rec := f.do(http.MethodGet, "/admin/staff?page=1&limit=2", owner, RoleOwner, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data       []StaffMember     `json:"data"`
		Pagination common.Pagination `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, common.Pagination{Page: 1, PerPage: 2, TotalItems: 3}, body.Pagination)
	require.Len(t, body.Data, 2)
	assert.Equal(t, RoleOwner, body.Data[0].Role)
	assert.Equal(t, RoleAdmin, body.Data[1].Role)

	rec = f.do(http.MethodGet, "/admin/staff?limit=1000", owner, RoleOwner, "")
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 100, body.Pagination.PerPage)
	assert.Len(t, body.Data, 3)
}

func TestRolesEndpointListsSevenRoles(t *testing.T) {
	f := newStaffFixture(t)
	rec := f.do(http.MethodGet, "/admin/roles", pgtype.UUID{Bytes: uuid.New(), Valid: true}, RoleAnalyst, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data []RoleDefinition `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 7)
	for i, r := range AllRoles() {
		assert.Equal(t, r, body.Data[i].Role)
		assert.Equal(t, PermissionsFor(r), body.Data[i].Permissions)
	}
}

type auditStub struct {
	inserted []dbgen.InsertAuditLogParams
}

func (s *auditStub) InsertAuditLog(_ context.Context, arg dbgen.InsertAuditLogParams) (dbgen.InsertAuditLogRow, error) {
	s.inserted = append(s.inserted, arg)
	return dbgen.InsertAuditLogRow{}, nil
}
func (s *auditStub) ListAuditLogs(context.Context, dbgen.ListAuditLogsParams) ([]dbgen.AuditLog, error) {
	return nil, nil
}
func (s *auditStub) ListAuditLogsFiltered(context.Context, dbgen.ListAuditLogsFilteredParams) ([]dbgen.AuditLog, error) {
	return nil, nil
}
func (s *auditStub) CountAuditLogsFiltered(context.Context, dbgen.CountAuditLogsFilteredParams) (int64, error) {
	return 0, nil
}

func TestStaffHandlersAnnotateAudit(t *testing.T) {
	f := newStaffFixture(t)
	owner := f.store.add("OWNER", "ACTIVE", "owner@a.test")
	sup := f.store.add("SUPPORT", "ACTIVE", "sup@a.test")
	store := &auditStub{}
	recorder := audit.HTTPRecorder{Service: &audit.Service{Store: store, Enabled: true, SamplingRate: 1}}
	f.router = recorder.Middleware(audit.HTTPConfig{ResourceType: "admin"})(f.router)

	rec := f.do(http.MethodPatch, "/admin/staff/"+uuidString(sup), owner, RoleOwner, `{"role":"analyst"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, store.inserted, 1)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(store.inserted[0].Metadata, &meta))
	assert.Equal(t, uuidString(sup), meta["targetUserId"])
	assert.Equal(t, "analyst", meta["newRole"])
	assert.Equal(t, "support", meta["previousRole"])
	assert.Equal(t, "sup@a.test", meta["email"])

	// A rejected change still records the target.
	rec = f.do(http.MethodDelete, "/admin/staff/"+uuidString(owner), owner, RoleOwner, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Len(t, store.inserted, 2)
	require.NoError(t, json.Unmarshal(store.inserted[1].Metadata, &meta))
	assert.Equal(t, uuidString(owner), meta["targetUserId"])
	assert.Equal(t, "customer", meta["newRole"])
	assert.Equal(t, int32(http.StatusConflict), store.inserted[1].Status)
}
