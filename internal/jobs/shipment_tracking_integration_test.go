package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/shipping"
	fx "github.com/noah-isme/backend-toko/internal/testutil/pgfixture"
)

// Run with: TEST_DATABASE_URL=<migrated db> go test ./internal/jobs/ -run ShipmentTrackingIntegration

// tenantScopedQueries limits the tenant loop to the test's tenant so other
// rows in the shared test database are not polled.
type tenantScopedQueries struct {
	*dbgen.Queries
	tenants []pgtype.UUID
}

func (q tenantScopedQueries) ListActiveTenantIDs(context.Context) ([]pgtype.UUID, error) {
	return q.tenants, nil
}

func seedShipment(t *testing.T, d *fx.DB, tenantID, userID pgtype.UUID, lastEventAt time.Time) (orderID, shipmentID pgtype.UUID) {
	t.Helper()
	order := d.Order(fx.OrderOpts{TenantID: tenantID, UserID: userID, Status: dbgen.OrderStatusSHIPPED})
	err := d.Pool.QueryRow(context.Background(), `INSERT INTO shipments(order_id, status, courier, tracking_number, last_status, last_event_at, tenant_id)
		VALUES ($1, 'SHIPPED', 'jne', $2, 'SHIPPED', $3, $4) RETURNING id`,
		order.ID, "AWB-"+fx.RandSuffix(), lastEventAt, tenantID).Scan(&shipmentID)
	require.NoError(t, err)
	return order.ID, shipmentID
}

func TestShipmentTrackingIntegration_DeliversAndStopsPolling(t *testing.T) {
	d := fx.Open(t)
	tenantID := d.Tenant()
	userID, _ := d.User()
	orderID, shipmentID := seedShipment(t, d, tenantID, userID, time.Now().Add(-2*time.Hour))
	_, staleID := seedShipment(t, d, tenantID, userID, time.Now().Add(-40*24*time.Hour))

	tracker := &fakeTracker{result: shipping.TrackResult{Events: []shipping.TrackEvent{
		{Status: "OUT_FOR_DELIVERY", Description: "WITH DELIVERY COURIER", Location: "KEDIRI", OccurredAt: time.Now().Add(-90 * time.Minute).Unix()},
		{Status: "DELIVERED", Description: "Diterima oleh BUDI", OccurredAt: time.Now().Add(-time.Hour).Unix()},
	}}}
	job := &ShipmentTrackingJob{
		Q:         tenantScopedQueries{Queries: d.Q, tenants: []pgtype.UUID{tenantID}},
		Svc:       &shipping.Service{Q: d.Q},
		Provider:  tracker,
		BatchSize: 10, MaxCallsPerTick: 10, MaxAttempts: 200, MaxAgeDays: 30,
		BackoffBase: 15 * time.Minute, BackoffMax: 6 * time.Hour, SilentAfter: 48 * time.Hour,
	}
	res, err := job.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, tracker.calls, "the stale shipment must not be polled")
	require.Equal(t, 1, res["stopped_max_age"])
	require.Equal(t, 2, res["events_applied"])
	require.Equal(t, 1, res["stopped_delivered"])

	require.Equal(t, "DELIVERED", d.OrderStatus(orderID))
	require.Equal(t, 2, d.Int(`SELECT count(*) FROM shipment_events WHERE shipment_id = $1`, shipmentID))
	require.Equal(t, "DELIVERED", d.Str(`SELECT last_status::text FROM shipments WHERE id = $1`, shipmentID))
	require.Equal(t, "stopped: delivered", d.Str(`SELECT track_last_error FROM shipments WHERE id = $1`, shipmentID))
	require.Equal(t, 0, d.Int(`SELECT count(*) FROM shipments WHERE id = $1 AND track_next_poll_at IS NOT NULL`, shipmentID))
	require.Equal(t, "stopped: max_age", d.Str(`SELECT track_last_error FROM shipments WHERE id = $1`, staleID))

	// A second run polls nothing.
	res, err = job.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, tracker.calls)
	require.Zero(t, res["polled"])
}

func TestShipmentTrackingIntegration_NoChangeBacksOff(t *testing.T) {
	d := fx.Open(t)
	tenantID := d.Tenant()
	userID, _ := d.User()
	_, shipmentID := seedShipment(t, d, tenantID, userID, time.Now().Add(-2*time.Hour))
	tracker := &fakeTracker{result: shipping.TrackResult{Events: []shipping.TrackEvent{{Status: "SHIPPED", OccurredAt: time.Now().Add(-2 * time.Hour).Unix()}}}}
	job := &ShipmentTrackingJob{
		Q:           tenantScopedQueries{Queries: d.Q, tenants: []pgtype.UUID{tenantID}},
		Svc:         &shipping.Service{Q: d.Q},
		Provider:    tracker,
		BackoffBase: 15 * time.Minute, BackoffMax: 6 * time.Hour,
	}
	res, err := job.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res["no_change"])
	require.Equal(t, 1, d.Int(`SELECT track_attempts FROM shipments WHERE id = $1`, shipmentID))
	require.Equal(t, 1, d.Int(`SELECT count(*) FROM shipments WHERE id = $1 AND track_next_poll_at BETWEEN now() + interval '29 minutes' AND now() + interval '31 minutes' AND track_last_polled_at IS NOT NULL`, shipmentID))
}
