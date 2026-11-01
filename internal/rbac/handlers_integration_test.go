package rbac_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/rbac"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// These tests drive the staff endpoints and guards against a real Postgres
// with all migrations applied. They are opt-in: set TEST_DATABASE_URL.

type staffITest struct {
	t       *testing.T
	pool    *pgxpool.Pool
	router  http.Handler
	tenants []string
	users   []string
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newStaffITest(t *testing.T) *staffITest {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	e := &staffITest{t: t, pool: pool}
	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM tenants WHERE id = ANY($1::uuid[])`, e.tenants); err != nil {
			t.Errorf("cleanup tenants: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, e.users); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})

	q := dbgen.New(pool)
	staff := &rbac.StaffHandler{Pool: pool, Q: q}
	probe := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if u := req.Header.Get("X-Test-User"); u != "" {
				ctx = common.WithUserID(ctx, u)
			}
			if tn := req.Header.Get("X-Test-Tenant"); tn != "" {
				ctx = tenant.WithTenant(ctx, tn)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/admin", func(a chi.Router) {
		a.Use(rbac.RequireMembership(q))
		a.Use(rbac.RequireStaff)
		a.Method(http.MethodGet, "/roles", rbac.Require(rbac.PermStaffRead, staff.Roles))
		a.Method(http.MethodGet, "/staff", rbac.Require(rbac.PermStaffRead, staff.List))
		a.Method(http.MethodPost, "/staff", rbac.Require(rbac.PermStaffManage, staff.Assign))
		a.Method(http.MethodPatch, "/staff/{userId}", rbac.Require(rbac.PermStaffManage, staff.ChangeRole))
		a.Method(http.MethodDelete, "/staff/{userId}", rbac.Require(rbac.PermStaffManage, staff.Revoke))
		a.Method(http.MethodPost, "/probe/catalog", rbac.Require(rbac.PermCatalogWrite, probe))
		a.Method(http.MethodPost, "/probe/orders", rbac.Require(rbac.PermOrdersWrite, probe))
	})
	e.router = r
	return e
}

func (e *staffITest) tenant() string {
	e.t.Helper()
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`INSERT INTO tenants(slug,name) VALUES($1,'RBAC IT') RETURNING id::text`, "rbac-it-"+randHex()).Scan(&id))
	e.tenants = append(e.tenants, id)
	return id
}

// user creates a user with the given global roles; email is returned too.
func (e *staffITest) user(globalRoles ...string) (string, string) {
	e.t.Helper()
	if len(globalRoles) == 0 {
		globalRoles = []string{"customer"}
	}
	email := "rbac-it-" + randHex() + "@example.test"
	var id string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		`INSERT INTO users(name,email,password_hash,roles) VALUES('RBAC IT',$1,'x',$2) RETURNING id::text`, email, globalRoles).Scan(&id))
	e.users = append(e.users, id)
	return id, email
}

func (e *staffITest) member(tenantID, userID, role, status string) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,$3,$4)`, tenantID, userID, role, status)
	require.NoError(e.t, err)
}

func (e *staffITest) roleOf(tenantID, userID string) string {
	e.t.Helper()
	var role string
	err := e.pool.QueryRow(context.Background(),
		`SELECT role FROM tenant_memberships WHERE tenant_id=$1 AND user_id=$2`, tenantID, userID).Scan(&role)
	if err != nil {
		return "<none>"
	}
	return role
}

func (e *staffITest) do(method, path, actor, tenantID, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Test-User", actor)
	req.Header.Set("X-Test-Tenant", tenantID)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

type apiError struct {
	Error struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) apiError {
	t.Helper()
	var body apiError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func TestIntegrationStaffLifecycleAndTenantIsolation(t *testing.T) {
	e := newStaffITest(t)
	tenA, tenB := e.tenant(), e.tenant()
	owner, _ := e.user()
	e.member(tenA, owner, "OWNER", "ACTIVE")
	cust, custEmail := e.user()
	e.member(tenA, cust, "CUSTOMER", "ACTIVE")
	otherOwner, _ := e.user()
	e.member(tenB, otherOwner, "OWNER", "ACTIVE")

	// (a) owner of A manages A. Email lookup is case-insensitive (citext).
	rec := e.do(http.MethodPost, "/admin/staff", owner, tenA, `{"email":"`+strings.ToUpper(custEmail)+`","role":"catalog"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "CATALOG", e.roleOf(tenA, cust))

	rec = e.do(http.MethodGet, "/admin/staff", owner, tenA, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Data       []rbac.StaffMember `json:"data"`
		Pagination common.Pagination  `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Data, 2)
	assert.Equal(t, rbac.RoleOwner, list.Data[0].Role)
	assert.Equal(t, rbac.RoleCatalog, list.Data[1].Role)
	assert.Equal(t, 2, list.Pagination.TotalItems)

	// (d) the new catalog user can write the catalog but not orders.
	assert.Equal(t, http.StatusOK, e.do(http.MethodPost, "/admin/probe/catalog", cust, tenA, "").Code)
	rec = e.do(http.MethodPost, "/admin/probe/orders", cust, tenA, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "orders:write", decode(t, rec).Error.Details["required"])

	rec = e.do(http.MethodPatch, "/admin/staff/"+cust, owner, tenA, `{"role":"support"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "SUPPORT", e.roleOf(tenA, cust))

	rec = e.do(http.MethodDelete, "/admin/staff/"+cust, owner, tenA, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, "CUSTOMER", e.roleOf(tenA, cust))

	// ...but has no membership, hence no access, in B.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/admin/staff", ""},
		{http.MethodPatch, "/admin/staff/" + otherOwner, `{"role":"manager"}`},
		{http.MethodDelete, "/admin/staff/" + otherOwner, ""},
	} {
		rec = e.do(c.method, c.path, owner, tenB, c.body)
		require.Equal(t, http.StatusForbidden, rec.Code, c.path)
		assert.Equal(t, "TENANT_FORBIDDEN", decode(t, rec).Error.Code)
	}
	// Targeting B's owner from A reads as "not staff here".
	rec = e.do(http.MethodPatch, "/admin/staff/"+otherOwner, owner, tenA, `{"role":"manager"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "STAFF_NOT_FOUND", decode(t, rec).Error.Code)
	assert.Equal(t, "OWNER", e.roleOf(tenB, otherOwner))
}

func TestIntegrationAssignOnlyExistingTenantMembers(t *testing.T) {
	e := newStaffITest(t)
	tenA, tenB := e.tenant(), e.tenant()
	owner, _ := e.user()
	e.member(tenA, owner, "OWNER", "ACTIVE")
	outsider, outsiderEmail := e.user()
	e.member(tenB, outsider, "CUSTOMER", "ACTIVE")

	rec := e.do(http.MethodPost, "/admin/staff", owner, tenA, `{"email":"`+outsiderEmail+`","role":"manager"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, "USER_NOT_FOUND", decode(t, rec).Error.Code)
	assert.NotContains(t, rec.Body.String(), "RBAC IT", "must not leak the user's name")
	assert.Equal(t, "<none>", e.roleOf(tenA, outsider), "no membership may be created")

	rec = e.do(http.MethodPost, "/admin/staff", owner, tenA, `{"email":"nobody-`+randHex()+`@example.test","role":"manager"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "USER_NOT_FOUND", decode(t, rec).Error.Code)
}

func TestIntegrationLastOwnerProtection(t *testing.T) {
	e := newStaffITest(t)
	ten := e.tenant()
	owner, _ := e.user()
	e.member(ten, owner, "OWNER", "ACTIVE")

	// (b) the sole owner cannot be demoted or revoked.
	rec := e.do(http.MethodPatch, "/admin/staff/"+owner, owner, ten, `{"role":"manager"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "LAST_OWNER", decode(t, rec).Error.Code)
	rec = e.do(http.MethodDelete, "/admin/staff/"+owner, owner, ten, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "OWNER", e.roleOf(ten, owner))

	// (c) with two owners one can be demoted.
	second, _ := e.user()
	e.member(ten, second, "OWNER", "ACTIVE")
	rec = e.do(http.MethodPatch, "/admin/staff/"+second, owner, ten, `{"role":"admin"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ADMIN", e.roleOf(ten, second))

	// An admin cannot touch the owner.
	rec = e.do(http.MethodDelete, "/admin/staff/"+owner, second, ten, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "OWNER_REQUIRED", decode(t, rec).Error.Code)
}

func TestIntegrationTenantRoleIsAuthoritative(t *testing.T) {
	e := newStaffITest(t)
	ten := e.tenant()
	globalAdmin, _ := e.user("customer", "admin")
	e.member(ten, globalAdmin, "CUSTOMER", "ACTIVE")

	// (e) a global admin whose membership here is CUSTOMER is not staff here.
	rec := e.do(http.MethodGet, "/admin/staff", globalAdmin, ten, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	body := decode(t, rec)
	assert.Equal(t, "FORBIDDEN", body.Error.Code)
	assert.Equal(t, "staff", body.Error.Details["required"])
}

func TestIntegrationSuspendedOwnerDemotion(t *testing.T) {
	e := newStaffITest(t)
	ten := e.tenant()
	active, _ := e.user()
	e.member(ten, active, "OWNER", "ACTIVE")
	suspended, _ := e.user()
	e.member(ten, suspended, "OWNER", "SUSPENDED")

	// (g) a suspended owner is not protected...
	rec := e.do(http.MethodPatch, "/admin/staff/"+suspended, active, ten, `{"role":"manager"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	// ...and never counted, so the active owner is still the last one.
	rec = e.do(http.MethodPatch, "/admin/staff/"+active, active, ten, `{"role":"manager"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "OWNER", e.roleOf(ten, active))
}

func TestIntegrationLastOwnerConcurrentDemotion(t *testing.T) {
	e := newStaffITest(t)
	for round := 0; round < 5; round++ {
		ten := e.tenant()
		a, _ := e.user()
		b, _ := e.user()
		e.member(ten, a, "OWNER", "ACTIVE")
		e.member(ten, b, "OWNER", "ACTIVE")

		start := make(chan struct{})
		codes := make([]int, 2)
		var wg sync.WaitGroup
		for i, target := range []string{a, b} {
			wg.Add(1)
			go func(i int, actor, target string) {
				defer wg.Done()
				<-start
				req := httptest.NewRequest(http.MethodPatch, "/admin/staff/"+target, strings.NewReader(`{"role":"manager"}`))
				req.Header.Set("X-Test-User", actor)
				req.Header.Set("X-Test-Tenant", ten)
				rec := httptest.NewRecorder()
				e.router.ServeHTTP(rec, req)
				codes[i] = rec.Code
			}(i, target, target)
		}
		close(start)
		wg.Wait()

		assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, codes, "round %d", round)
		var owners int
		require.NoError(t, e.pool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM tenant_memberships WHERE tenant_id=$1 AND role='OWNER' AND status='ACTIVE'`, ten).Scan(&owners))
		assert.Equal(t, 1, owners, "round %d", round)
	}
}

// TestIntegrationConcurrentDemotionOfActor covers the time-of-check race: an
// owner demoted while their own request waits on the staff lock must not
// complete an owner-only action with the stale role.
func TestIntegrationConcurrentDemotionOfActor(t *testing.T) {
	e := newStaffITest(t)
	ten := e.tenant()
	x, _ := e.user()
	y, _ := e.user()
	z, zEmail := e.user()
	e.member(ten, x, "OWNER", "ACTIVE")
	e.member(ten, y, "OWNER", "ACTIVE")
	e.member(ten, z, "CUSTOMER", "ACTIVE")

	// Hold the tenant staff lock so Y's request passes RequireMembership as
	// owner and then blocks inside its transaction.
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('tenant_staff:' || $1::uuid::text, 0))`, ten)
	require.NoError(t, err)

	done := make(chan int, 1)
	go func() {
		rec := e.do(http.MethodPost, "/admin/staff", y, ten, `{"email":"`+zEmail+`","role":"owner"}`)
		done <- rec.Code
	}()
	// Wait until Y's transaction is blocked on the advisory lock.
	require.Eventually(t, func() bool {
		var waiting int
		_ = e.pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_locks l JOIN pg_database d ON d.oid = l.database
			 WHERE l.locktype='advisory' AND NOT l.granted AND d.datname = current_database()`).Scan(&waiting)
		return waiting > 0
	}, 5*time.Second, 10*time.Millisecond)
	// X demotes Y while Y waits, then releases the lock.
	_, err = tx.Exec(ctx, `UPDATE tenant_memberships SET role='MANAGER' WHERE tenant_id=$1 AND user_id=$2`, ten, y)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	code := <-done
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "CUSTOMER", e.roleOf(ten, z), "Z must not become owner")
}
