package admin

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// statsQuerier implements only the queries the overview and order-stats
// handlers call; any other method panics through the nil embedded interface.
type statsQuerier struct {
	dbgen.Querier
	stats  dbgen.AdminOrderStatsRow
	statsP []dbgen.AdminOrderStatsParams
	topP   []dbgen.AdminTopProductsByRevenueParams
}

func (q *statsQuerier) AdminOrderStats(_ context.Context, arg dbgen.AdminOrderStatsParams) (dbgen.AdminOrderStatsRow, error) {
	q.statsP = append(q.statsP, arg)
	return q.stats, nil
}

func (q *statsQuerier) AdminCountCustomers(context.Context, pgtype.UUID) (int64, error) {
	return 0, nil
}

func (q *statsQuerier) AdminCountProductsTotal(context.Context, pgtype.UUID) (int64, error) {
	return 0, nil
}

func (q *statsQuerier) AdminTopProductsByRevenue(_ context.Context, arg dbgen.AdminTopProductsByRevenueParams) ([]dbgen.AdminTopProductsByRevenueRow, error) {
	q.topP = append(q.topP, arg)
	return nil, nil
}

func (q *statsQuerier) GetTenantSetting(context.Context, dbgen.GetTenantSettingParams) ([]byte, error) {
	return nil, pgx.ErrNoRows
}

func (q *statsQuerier) AdminStockStatusCounts(context.Context, dbgen.AdminStockStatusCountsParams) (dbgen.AdminStockStatusCountsRow, error) {
	return dbgen.AdminStockStatusCountsRow{}, nil
}

var sampleStats = dbgen.AdminOrderStatsRow{TotalOrders: 7, TotalRevenue: 495000, RevenueOrders: 4, PendingOrders: 1, PaidOrders: 2, DeliveredOrders: 2, CancelledOrders: 1, AverageOrderValue: 123750}

func TestOrderStatsExposesRevenueOrders(t *testing.T) {
	q := &statsQuerier{stats: sampleStats}
	h := &OrdersHandler{Q: q}
	tenantID := uuid.NewString()
	rec := serveReport(h.OrderStats, "/admin/orders/stats?startDate=2026-10-01&endDate=2026-10-03", tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data orderStatsDTO `json:"data"`
	}
	decodeJSON(t, rec, &body)
	if body.Data.RevenueOrders != 4 || body.Data.TotalRevenue != 495000 || body.Data.AverageOrderValue != 123750 || body.Data.TotalOrders != 7 {
		t.Fatalf("unexpected body: %+v", body.Data)
	}
	arg := q.statsP[0]
	// startDate/endDate keep their UTC-midnight semantics (documented).
	if uuidString(arg.TenantID) != tenantID || arg.StartAt.Time.Format("2006-01-02T15:04Z07:00") != "2026-10-01T00:00Z" || !arg.EndAt.Valid {
		t.Fatalf("unexpected params: %+v", arg)
	}
}

func TestAnalyticsOverviewPassesEndAndRevenueOrders(t *testing.T) {
	q := &statsQuerier{stats: sampleStats}
	h := &AnalyticsHandler{Q: q}
	rec := serveReport(h.Overview, "/admin/analytics/overview?range=7d", uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data analyticsOverviewDTO `json:"data"`
	}
	decodeJSON(t, rec, &body)
	if body.Data.RevenueOrders != 4 || body.Data.AverageOrderValue != 123750 {
		t.Fatalf("unexpected body: %+v", body.Data)
	}
	if !q.statsP[0].StartAt.Valid || q.statsP[0].EndAt.Valid || !q.topP[0].StartAt.Valid || q.topP[0].EndAt.Valid {
		t.Fatalf("rolling window must pass start and an unbounded end: %+v %+v", q.statsP[0], q.topP[0])
	}
}
