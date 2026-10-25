package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/rbac"
)

const platformGuard = "platform-admin"

// expectedAdminRoutes is the permission table of the API contract
// (docs/contracts/admin.md "Permissions per endpoint"). Adding, removing,
// renaming or re-methoding an /admin or /analytics route must update it.
var expectedAdminRoutes = map[string]string{
	"POST /api/v1/admin/vouchers":                            "promotions:write",
	"PUT /api/v1/admin/vouchers/{code}":                      "promotions:write",
	"POST /api/v1/admin/vouchers/preview":                    "promotions:read",
	"GET /api/v1/admin/vouchers":                             "promotions:read",
	"GET /api/v1/admin/vouchers/stats":                       "promotions:read",
	"DELETE /api/v1/admin/vouchers/{code}":                   "promotions:write",
	"POST /api/v1/admin/flash-sales":                         "promotions:write",
	"GET /api/v1/admin/flash-sales":                          "promotions:read",
	"GET /api/v1/admin/flash-sales/{id}":                     "promotions:read",
	"PATCH /api/v1/admin/flash-sales/{id}":                   "promotions:write",
	"GET /api/v1/admin/orders":                               "orders:read",
	"GET /api/v1/admin/orders/stats":                         "orders:read",
	"GET /api/v1/admin/orders/{id}":                          "orders:read",
	"POST /api/v1/admin/orders/{id}/shipment":                "orders:write",
	"PATCH /api/v1/admin/orders/{id}/status":                 "orders:write",
	"GET /api/v1/admin/customers":                            "customers:read",
	"GET /api/v1/admin/newsletter/subscribers":               "customers:read",
	"GET /api/v1/admin/inventory":                            "inventory:read",
	"PATCH /api/v1/admin/inventory/{variantId}":              "inventory:write",
	"GET /api/v1/admin/returns":                              "returns:read",
	"PATCH /api/v1/admin/returns/{returnId}":                 "returns:write",
	"POST /api/v1/admin/returns/{returnId}/refund":           "refunds:write",
	"GET /api/v1/admin/support/tickets":                      "support:read",
	"GET /api/v1/admin/support/tickets/{ticketId}/messages":  "support:read",
	"PATCH /api/v1/admin/support/tickets/{ticketId}":         "support:write",
	"POST /api/v1/admin/support/tickets/{ticketId}/messages": "support:write",
	"GET /api/v1/admin/settings":                             "settings:read",
	"PATCH /api/v1/admin/settings":                           "settings:write",
	"POST /api/v1/admin/onboarding":                          "settings:write",
	"GET /api/v1/admin/products":                             "catalog:read",
	"POST /api/v1/admin/products":                            "catalog:write",
	"GET /api/v1/admin/products/{id}":                        "catalog:read",
	"PATCH /api/v1/admin/products/{id}":                      "catalog:write",
	"DELETE /api/v1/admin/products/{id}":                     "catalog:write",
	"PATCH /api/v1/admin/products/{id}/stock":                "inventory:write",
	"GET /api/v1/admin/categories":                           "catalog:read",
	"POST /api/v1/admin/categories":                          "catalog:write",
	"PATCH /api/v1/admin/categories/{id}":                    "catalog:write",
	"DELETE /api/v1/admin/categories/{id}":                   "catalog:write",
	"GET /api/v1/admin/brands":                               "catalog:read",
	"POST /api/v1/admin/brands":                              "catalog:write",
	"PATCH /api/v1/admin/brands/{id}":                        "catalog:write",
	"DELETE /api/v1/admin/brands/{id}":                       "catalog:write",
	"GET /api/v1/admin/analytics/overview":                   "analytics:read",
	"GET /api/v1/admin/roles":                                "staff:read",
	"GET /api/v1/admin/staff":                                "staff:read",
	"POST /api/v1/admin/staff":                               "staff:manage",
	"PATCH /api/v1/admin/staff/{userId}":                     "staff:manage",
	"DELETE /api/v1/admin/staff/{userId}":                    "staff:manage",
	"POST /api/v1/admin/webhooks":                            platformGuard,
	"PUT /api/v1/admin/webhooks/{id}":                        platformGuard,
	"GET /api/v1/admin/webhooks":                             platformGuard,
	"DELETE /api/v1/admin/webhooks/{id}":                     platformGuard,
	"GET /api/v1/admin/webhook-deliveries":                   platformGuard,
	"POST /api/v1/admin/webhook-deliveries/{id}/replay":      platformGuard,
	"GET /api/v1/admin/queue/dlq":                            platformGuard,
	"POST /api/v1/admin/queue/dlq/replay":                    platformGuard,
	"GET /api/v1/admin/queue/stats":                          platformGuard,
	"GET /api/v1/admin/audit-logs":                           platformGuard,
	"GET /api/v1/analytics/sales":                            platformGuard,
	"GET /api/v1/analytics/top-products":                     platformGuard,
	"GET /api/v1/analytics/overview":                         platformGuard,
}

func passThrough(next http.Handler) http.Handler { return next }

// roleFromHeader stands in for RequireMembership: the test picks the role.
func roleFromHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if role, ok := rbac.ParseRole(r.Header.Get("X-Test-Role")); ok {
			r = r.WithContext(rbac.WithRole(r.Context(), role))
		}
		next.ServeHTTP(w, r)
	})
}

func buildAdminRouter(d adminDeps) chi.Router {
	r := chi.NewRouter()
	r.Route("/api/v1/admin", func(a chi.Router) { registerAdminRoutes(a, d) })
	r.Route("/api/v1/analytics", func(a chi.Router) { registerAnalyticsRoutes(a, d) })
	return r
}

func testDeps() adminDeps {
	// Handler pointers stay nil: method values on nil pointers are legal and
	// the walk never invokes them.
	return adminDeps{RequireAuth: passThrough, Membership: roleFromHeader, PlatformAdmin: passThrough}
}

// collectGuards walks r and returns "METHOD pattern" -> permission (or
// platformGuard) plus one error per endpoint that is not an rbac.Guarded.
func collectGuards(r chi.Routes) (map[string]string, []string) {
	got := map[string]string{}
	var problems []string
	_ = chi.Walk(r, func(method, route string, h http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + strings.TrimSuffix(route, "/")
		if _, dup := got[key]; dup {
			problems = append(problems, "duplicate route "+key)
		}
		g, ok := h.(rbac.Guarded)
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s is not wrapped in rbac.Require/rbac.RequirePlatform (%T)", key, h))
			got[key] = "UNGUARDED"
		case g.Platform:
			got[key] = platformGuard
		case g.Permission != "":
			got[key] = string(g.Permission)
		default:
			problems = append(problems, key+" has an empty guard")
			got[key] = "EMPTY"
		}
		return nil
	})
	return got, problems
}

func TestAdminRoutesAreGuarded(t *testing.T) {
	got, problems := collectGuards(buildAdminRouter(testDeps()))
	assert.Empty(t, problems)
	assert.Equal(t, expectedAdminRoutes, got)
	assert.Len(t, got, 62)
}

func TestAdminRouteGuardDetectsUnwrappedRoute(t *testing.T) {
	r := buildAdminRouter(testDeps())
	r.Route("/api/v1/admin-extra", func(a chi.Router) {
		a.Get("/x", func(http.ResponseWriter, *http.Request) {})
	})
	_, problems := collectGuards(r)
	require.Len(t, problems, 1)
	assert.Contains(t, problems[0], "GET /api/v1/admin-extra/x")
}

var routeParam = regexp.MustCompile(`\{[^}]+\}`)

func concretePath(pattern string) string {
	return routeParam.ReplaceAllString(pattern, "00000000-0000-4000-8000-000000000001")
}

// TestAdminRoutesDenyCustomersAndAuditMutations drives every walked route
// through the real middleware chain: a customer member is stopped by
// RequireStaff and that probe is audited, and every mutating request of a
// staff member is audited even when the permission check denies it.
func TestAdminRoutesDenyCustomersAndAuditMutations(t *testing.T) {
	audited := map[string]int{}
	d := testDeps()
	d.Audit = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			// Keyed by the concrete path: a request RequireStaff denies never
			// reaches the inner router, so its route pattern is incomplete.
			audited[r.Method+" "+r.URL.Path]++
		})
	}
	r := buildAdminRouter(d)

	keys := make([]string, 0, len(expectedAdminRoutes))
	for k := range expectedAdminRoutes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		method, pattern, _ := strings.Cut(key, " ")
		path := concretePath(pattern)

		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Test-Role", "customer")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "customer on %s", key)
		assert.Contains(t, rec.Body.String(), `"required":"staff"`, key)
		if strings.HasPrefix(pattern, "/api/v1/admin") {
			assert.Equal(t, 1, audited[method+" "+path], "customer probe of %s must be audited", key)
		}

		if method == http.MethodGet || strings.HasPrefix(pattern, "/api/v1/analytics") {
			continue
		}
		// A staff role lacking the route's permission (and never carrying the
		// platform flag) is denied by the guard after the audit middleware ran.
		role := staffRoleWithout(t, rbac.Permission(expectedAdminRoutes[key]))
		req = httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Test-Role", string(role))
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s on %s", role, key)
		assert.Equal(t, 2, audited[method+" "+path], "mutation %s must be audited", key)
	}
}

func staffRoleWithout(t *testing.T, p rbac.Permission) rbac.Role {
	t.Helper()
	for _, role := range rbac.AllRoles() {
		if !role.Has(p) {
			return role
		}
	}
	t.Fatalf("every staff role holds %s", p)
	return ""
}
