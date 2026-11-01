package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/cart"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// TestIntegrationWebhookEndpointsAreTenantOwned covers POST /admin/webhooks:
// the endpoint is stored with the request's tenant, so the tenant-scoped
// dispatcher delivers that tenant's events to it, and another tenant can
// neither list, update nor delete it.
func TestIntegrationWebhookEndpointsAreTenantOwned(t *testing.T) {
	e := newIsolationITest(t)
	e.exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'OWNER','ACTIVE')`, e.tenB, e.owner)
	// Webhook routes are platform-admin routes.
	e.exec(`UPDATE users SET roles = roles || '{admin}' WHERE id=$1`, e.owner)
	t.Cleanup(func() {
		ctx := context.Background()
		tenants := []string{e.tenA, e.tenB}
		for _, stmt := range []string{
			`DELETE FROM webhook_deliveries WHERE endpoint_id IN (SELECT id FROM webhook_endpoints WHERE tenant_id = ANY($1::uuid[]))`,
			`DELETE FROM domain_events WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM webhook_endpoints WHERE tenant_id = ANY($1::uuid[])`,
		} {
			if _, err := e.pool.Exec(ctx, stmt, tenants); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})

	rec := e.do(http.MethodPost, "/api/v1/admin/webhooks", e.owner, e.tenA, `{"name":"A hook","url":"https://example.test/a","secret":"s","topics":[]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var created dbgen.WebhookEndpoint
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	epA := uuid.UUID(created.ID.Bytes).String()
	assert.Equal(t, e.tenA, e.scalar(`SELECT tenant_id::text FROM webhook_endpoints WHERE id=$1`, epA))

	// The delivery query selects it for tenant A only.
	tenA, err := cart.ToUUID(e.tenA)
	require.NoError(t, err)
	tenB, err := cart.ToUUID(e.tenB)
	require.NoError(t, err)
	selected, err := e.q.ListActiveEndpointsForTopicAndTenant(context.Background(), dbgen.ListActiveEndpointsForTopicAndTenantParams{TenantID: tenA, Topic: events.TopicOrderPaid})
	require.NoError(t, err)
	require.Len(t, selected, 1)
	assert.Equal(t, created.ID, selected[0].ID)
	selected, err = e.q.ListActiveEndpointsForTopicAndTenant(context.Background(), dbgen.ListActiveEndpointsForTopicAndTenantParams{TenantID: tenB, Topic: events.TopicOrderPaid})
	require.NoError(t, err)
	assert.Empty(t, selected)

	// End to end: tenant A's event is scheduled for the endpoint, tenant B's is not.
	bus := &events.Bus{Store: e.q, Scheduler: &notify.Dispatcher{Store: notify.QueriesStore{Queries: e.q}, Enabled: true}}
	evA, err := bus.Emit(tenant.WithTenant(context.Background(), e.tenA), events.TopicOrderPaid, pgtype.UUID{Bytes: uuid.New(), Valid: true}, map[string]any{"orderId": "it"})
	require.NoError(t, err)
	evB, err := bus.Emit(tenant.WithTenant(context.Background(), e.tenB), events.TopicOrderPaid, pgtype.UUID{Bytes: uuid.New(), Valid: true}, map[string]any{"orderId": "it"})
	require.NoError(t, err)
	assert.Equal(t, "1", e.scalar(`SELECT COUNT(*)::text FROM webhook_deliveries WHERE endpoint_id=$1 AND event_id=$2`, epA, evA.ID))
	assert.Equal(t, "0", e.scalar(`SELECT COUNT(*)::text FROM webhook_deliveries WHERE endpoint_id=$1 AND event_id=$2`, epA, evB.ID))

	// Tenant B cannot see, change or delete it.
	rec = e.do(http.MethodGet, "/api/v1/admin/webhooks", e.owner, e.tenB, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), epA)
	rec = e.do(http.MethodPut, "/api/v1/admin/webhooks/"+epA, e.owner, e.tenB, `{"name":"taken","url":"https://example.test/b","secret":"s"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	rec = e.do(http.MethodDelete, "/api/v1/admin/webhooks/"+epA, e.owner, e.tenB, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, "A hook|https://example.test/a", e.scalar(`SELECT name || '|' || url FROM webhook_endpoints WHERE id=$1`, epA))

	// Tenant A lists, updates and deletes its own endpoint.
	rec = e.do(http.MethodGet, "/api/v1/admin/webhooks", e.owner, e.tenA, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), epA)
	rec = e.do(http.MethodPut, "/api/v1/admin/webhooks/"+epA, e.owner, e.tenA, `{"name":"A hook 2","url":"https://example.test/a2","secret":"s"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = e.do(http.MethodDelete, "/api/v1/admin/webhooks/"+epA, e.owner, e.tenA, "")
	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Equal(t, "0", e.scalar(`SELECT COUNT(*)::text FROM webhook_endpoints WHERE id=$1`, epA))
}
