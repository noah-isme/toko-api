package rbac

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type fakeRoleStore struct {
	role string
	err  error
	got  dbgen.GetTenantMembershipRoleParams
}

func (f *fakeRoleStore) GetTenantMembershipRole(_ context.Context, arg dbgen.GetTenantMembershipRoleParams) (string, error) {
	f.got = arg
	return f.role, f.err
}

type fakeUserStore struct {
	roles []string
	err   error
}

func (f *fakeUserStore) GetUserByID(_ context.Context, id pgtype.UUID) (dbgen.GetUserByIDRow, error) {
	return dbgen.GetUserByIDRow{ID: id, Roles: f.roles}, f.err
}

type errorBody struct {
	Error struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func serve(h http.Handler, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequireMembership(t *testing.T) {
	userID, tenantID := uuid.New(), uuid.New()
	withBoth := tenant.WithTenant(common.WithUserID(context.Background(), userID.String()), tenantID.String())

	t.Run("missing user", func(t *testing.T) {
		rec := serve(RequireMembership(&fakeRoleStore{})(http.HandlerFunc(okHandler)), tenant.WithTenant(context.Background(), tenantID.String()))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
	t.Run("missing tenant", func(t *testing.T) {
		rec := serve(RequireMembership(&fakeRoleStore{})(http.HandlerFunc(okHandler)), common.WithUserID(context.Background(), userID.String()))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
	t.Run("bad uuid", func(t *testing.T) {
		ctx := tenant.WithTenant(common.WithUserID(context.Background(), "nope"), tenantID.String())
		rec := serve(RequireMembership(&fakeRoleStore{})(http.HandlerFunc(okHandler)), ctx)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
	t.Run("nil store", func(t *testing.T) {
		rec := serve(RequireMembership(nil)(http.HandlerFunc(okHandler)), withBoth)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("not a member", func(t *testing.T) {
		rec := serve(RequireMembership(&fakeRoleStore{err: pgx.ErrNoRows})(http.HandlerFunc(okHandler)), withBoth)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, "TENANT_FORBIDDEN", decodeError(t, rec).Error.Code)
	})
	t.Run("db error", func(t *testing.T) {
		rec := serve(RequireMembership(&fakeRoleStore{err: errors.New("boom")})(http.HandlerFunc(okHandler)), withBoth)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("member gets role on context", func(t *testing.T) {
		store := &fakeRoleStore{role: "MANAGER"}
		var seen Role
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen, _ = RoleFromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		})
		rec := serve(RequireMembership(store)(next), withBoth)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, RoleManager, seen)
		assert.Equal(t, pgtype.UUID{Bytes: tenantID, Valid: true}, store.got.TenantID)
		assert.Equal(t, pgtype.UUID{Bytes: userID, Valid: true}, store.got.UserID)
	})
	t.Run("unknown db role becomes customer", func(t *testing.T) {
		var seen Role
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen, _ = RoleFromContext(r.Context())
		})
		serve(RequireMembership(&fakeRoleStore{role: "SUPERUSER"})(next), withBoth)
		assert.Equal(t, RoleCustomer, seen)
	})
}

func TestRequireStaff(t *testing.T) {
	h := RequireStaff(http.HandlerFunc(okHandler))

	rec := serve(h, context.Background())
	require.Equal(t, http.StatusForbidden, rec.Code)
	body := decodeError(t, rec)
	assert.Equal(t, "FORBIDDEN", body.Error.Code)
	assert.Equal(t, RequiredStaff, body.Error.Details["required"])

	rec = serve(h, WithRole(context.Background(), RoleCustomer))
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, RequiredStaff, decodeError(t, rec).Error.Details["required"])

	for _, r := range AllRoles() {
		rec = serve(h, WithRole(context.Background(), r))
		assert.Equal(t, http.StatusOK, rec.Code, string(r))
	}
}

func TestRequirePlatformAdmin(t *testing.T) {
	ctx := common.WithUserID(context.Background(), uuid.NewString())

	rec := serve(RequirePlatformAdmin(&fakeUserStore{roles: []string{"customer"}})(http.HandlerFunc(okHandler)), ctx)
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, RequiredPlatformAdmin, decodeError(t, rec).Error.Details["required"])

	var flagged bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flagged = IsPlatformAdmin(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	rec = serve(RequirePlatformAdmin(&fakeUserStore{roles: []string{"customer", "admin"}})(next), ctx)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, flagged)

	rec = serve(RequirePlatformAdmin(&fakeUserStore{err: errors.New("boom")})(http.HandlerFunc(okHandler)), ctx)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	rec = serve(RequirePlatformAdmin(&fakeUserStore{err: pgx.ErrNoRows})(http.HandlerFunc(okHandler)), ctx)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	rec = serve(RequirePlatformAdmin(&fakeUserStore{roles: []string{"admin"}})(http.HandlerFunc(okHandler)), context.Background())
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	rec = serve(RequirePlatformAdmin(nil)(http.HandlerFunc(okHandler)), ctx)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGuarded(t *testing.T) {
	g := Require(PermOrdersWrite, okHandler)

	rec := serve(g, WithRole(context.Background(), RoleCatalog))
	require.Equal(t, http.StatusForbidden, rec.Code)
	body := decodeError(t, rec)
	assert.Equal(t, "FORBIDDEN", body.Error.Code)
	assert.Equal(t, "insufficient permissions", body.Error.Message)
	assert.Equal(t, "orders:write", body.Error.Details["required"])

	assert.Equal(t, http.StatusOK, serve(g, WithRole(context.Background(), RoleFulfillment)).Code)
	assert.Equal(t, http.StatusOK, serve(g, WithRole(context.Background(), RoleOwner)).Code)
	assert.Equal(t, http.StatusForbidden, serve(g, context.Background()).Code)
	// The platform flag never substitutes for a tenant permission.
	assert.Equal(t, http.StatusForbidden, serve(g, WithPlatformAdmin(context.Background())).Code)

	p := RequirePlatform(okHandler)
	rec = serve(p, WithRole(context.Background(), RoleOwner))
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, RequiredPlatformAdmin, decodeError(t, rec).Error.Details["required"])
	assert.Equal(t, http.StatusOK, serve(p, WithPlatformAdmin(WithRole(context.Background(), RoleOwner))).Code)

	assert.Panics(t, func() { Require("", okHandler) })
	assert.Equal(t, http.StatusInternalServerError, serve(Require(PermOrdersRead, nil), WithRole(context.Background(), RoleOwner)).Code)
}
