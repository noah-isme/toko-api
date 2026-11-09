package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type expiredStatsQuerier struct {
	dbgen.Querier
	stats dbgen.AdminOrderStatsRow
}

func (s expiredStatsQuerier) AdminOrderStats(context.Context, dbgen.AdminOrderStatsParams) (dbgen.AdminOrderStatsRow, error) {
	return s.stats, nil
}

func TestOrderStatsIncludesExpiredOrders(t *testing.T) {
	h := &OrdersHandler{Q: expiredStatsQuerier{stats: dbgen.AdminOrderStatsRow{TotalOrders: 5, CancelledOrders: 1, ExpiredOrders: 2}}}
	rec := httptest.NewRecorder()
	h.OrderStats(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/orders/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data["expiredOrders"] != float64(2) || body.Data["cancelledOrders"] != float64(1) {
		t.Fatalf("unexpected stats payload: %v", body.Data)
	}
}
