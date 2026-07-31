package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// AnalyticsHandler serves the dashboard overview cards and top-product table.
type AnalyticsHandler struct {
	Q dbgen.Querier
}

type analyticsTopProductDTO struct {
	ProductID string `json:"productId"`
	Title     string `json:"title"`
	Slug      string `json:"slug"`
	UnitsSold int64  `json:"unitsSold"`
	Revenue   int64  `json:"revenue"`
}

type analyticsOverviewDTO struct {
	Range             string                   `json:"range"`
	TotalRevenue      int64                    `json:"totalRevenue"`
	TotalOrders       int64                    `json:"totalOrders"`
	TotalCustomers    int64                    `json:"totalCustomers"`
	TotalProducts     int64                    `json:"totalProducts"`
	AverageOrderValue int64                    `json:"averageOrderValue"`
	PendingOrders     int64                    `json:"pendingOrders"`
	PaidOrders        int64                    `json:"paidOrders"`
	ShippedOrders     int64                    `json:"shippedOrders"`
	DeliveredOrders   int64                    `json:"deliveredOrders"`
	CancelledOrders   int64                    `json:"cancelledOrders"`
	TopProducts       []analyticsTopProductDTO `json:"topProducts"`
}

// rangeWindow maps a UI range token (7d, 30d, 90d, all) to a start timestamp.
func rangeWindow(raw string) (string, pgtype.Timestamptz) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		normalized = "30d"
	}
	if normalized == "all" {
		return "all", pgtype.Timestamptz{}
	}
	days := 30
	if strings.HasSuffix(normalized, "d") {
		if parsed, err := strconv.Atoi(strings.TrimSuffix(normalized, "d")); err == nil && parsed > 0 {
			days = parsed
		} else {
			normalized = "30d"
		}
	} else {
		normalized = "30d"
	}
	start := time.Now().UTC().AddDate(0, 0, -days)
	return normalized, pgtype.Timestamptz{Time: start, Valid: true}
}

// Overview handles GET /api/v1/admin/analytics/overview.
func (h *AnalyticsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	label, startAt := rangeWindow(r.URL.Query().Get("range"))

	ctx := r.Context()
	stats, err := h.Q.AdminOrderStats(ctx, dbgen.AdminOrderStatsParams{StartAt: startAt})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order stats", nil)
		return
	}
	customers, err := h.Q.AdminCountCustomers(ctx)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count customers", nil)
		return
	}
	products, err := h.Q.AdminCountProductsTotal(ctx)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count products", nil)
		return
	}
	topRows, err := h.Q.AdminTopProductsByRevenue(ctx, dbgen.AdminTopProductsByRevenueParams{
		StartAt:    startAt,
		LimitValue: 5,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load top products", nil)
		return
	}
	top := make([]analyticsTopProductDTO, 0, len(topRows))
	for _, row := range topRows {
		top = append(top, analyticsTopProductDTO{
			ProductID: uuidString(row.ProductID),
			Title:     row.Title,
			Slug:      row.Slug,
			UnitsSold: row.UnitsSold,
			Revenue:   row.Revenue,
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": analyticsOverviewDTO{
		Range:             label,
		TotalRevenue:      stats.TotalRevenue,
		TotalOrders:       stats.TotalOrders,
		TotalCustomers:    customers,
		TotalProducts:     products,
		AverageOrderValue: stats.AverageOrderValue,
		PendingOrders:     stats.PendingOrders,
		PaidOrders:        stats.PaidOrders,
		ShippedOrders:     stats.ShippedOrders,
		DeliveredOrders:   stats.DeliveredOrders,
		CancelledOrders:   stats.CancelledOrders,
		TopProducts:       top,
	}})
}
