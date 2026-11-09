package admin

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

const (
	reportDefaultDays = 30
	reportMaxDays     = 366
)

// reportsQuerier is the slice of dbgen.Querier the admin reports need.
type reportsQuerier interface {
	GetTenantTimezone(ctx context.Context, id pgtype.UUID) (string, error)
	AdminReportSalesByCategory(ctx context.Context, arg dbgen.AdminReportSalesByCategoryParams) ([]dbgen.AdminReportSalesByCategoryRow, error)
	AdminReportSalesByProduct(ctx context.Context, arg dbgen.AdminReportSalesByProductParams) ([]dbgen.AdminReportSalesByProductRow, error)
	AdminReportSalesByProductCount(ctx context.Context, arg dbgen.AdminReportSalesByProductCountParams) (int64, error)
	AdminReportConversion(ctx context.Context, arg dbgen.AdminReportConversionParams) (dbgen.AdminReportConversionRow, error)
	AdminReportCustomersSummary(ctx context.Context, arg dbgen.AdminReportCustomersSummaryParams) (dbgen.AdminReportCustomersSummaryRow, error)
	AdminReportCustomersDaily(ctx context.Context, arg dbgen.AdminReportCustomersDailyParams) ([]dbgen.AdminReportCustomersDailyRow, error)
}

// ReportsHandler serves the tenant-scoped admin reports under
// /api/v1/admin/reports (JSON, or CSV with format=csv). The revenue rule and
// field definitions are documented in docs/contracts/analytics.md.
type ReportsHandler struct {
	Q   reportsQuerier
	Now func() time.Time
}

func (h *ReportsHandler) now() time.Time {
	if h != nil && h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// reportScope is the resolved tenant, timezone and range of one request.
type reportScope struct {
	tenantID pgtype.UUID
	loc      *time.Location
	from     time.Time
	to       time.Time
}

func (s reportScope) start() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: s.from, Valid: true}
}

func (s reportScope) end() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: s.to, Valid: true}
}

func (s reportScope) fromString() string { return s.from.In(s.loc).Format(time.RFC3339) }
func (s reportScope) toString() string   { return s.to.In(s.loc).Format(time.RFC3339) }

type reportRangeDTO struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
}

func (s reportScope) rangeDTO() reportRangeDTO {
	return reportRangeDTO{From: s.fromString(), To: s.toString(), Timezone: s.loc.String()}
}

// reportContext resolves the tenant and its timezone. It writes the error
// response and returns ok=false when the request cannot be served.
func (h *ReportsHandler) reportContext(w http.ResponseWriter, r *http.Request) (pgtype.UUID, *time.Location, bool) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return pgtype.UUID{}, nil, false
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	if !tenantID.Valid {
		common.JSONError(w, http.StatusBadRequest, "TENANT_REQUIRED", "tenant is required", nil)
		return pgtype.UUID{}, nil, false
	}
	name, err := h.Q.GetTenantTimezone(ctx, tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusBadRequest, "TENANT_REQUIRED", "tenant is required", nil)
			return pgtype.UUID{}, nil, false
		}
		zerolog.Ctx(ctx).Error().Err(err).Msg("load tenant timezone")
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load tenant", nil)
		return pgtype.UUID{}, nil, false
	}
	loc, ok := tenant.Location(name)
	if !ok {
		zerolog.Ctx(ctx).Warn().Str("tenant_id", uuidString(tenantID)).Str("timezone", name).Msg("invalid tenant timezone, falling back to UTC")
	}
	return tenantID, loc, true
}

// scope resolves tenant, timezone, range and output format for a report.
func (h *ReportsHandler) scope(w http.ResponseWriter, r *http.Request) (reportScope, bool, bool) {
	csvOut, err := reportFormat(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return reportScope{}, false, false
	}
	tenantID, loc, ok := h.reportContext(w, r)
	if !ok {
		return reportScope{}, false, false
	}
	from, to, err := parseReportRange(r, h.now(), loc)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return reportScope{}, false, false
	}
	return reportScope{tenantID: tenantID, loc: loc, from: from, to: to}, csvOut, true
}

func reportFormat(r *http.Request) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))) {
	case "", "json":
		return false, nil
	case "csv":
		return true, nil
	default:
		return false, errors.New("format must be json or csv")
	}
}

// parseReportRange reads from/to: RFC3339 is used as-is, YYYY-MM-DD is
// midnight in loc. Defaults: to = start of tomorrow in loc, from = to - 30
// days. The range is [from, to), must be non-empty and at most 366 days.
func parseReportRange(r *http.Request, now time.Time, loc *time.Location) (time.Time, time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	q := r.URL.Query()
	from, hasFrom, err := parseReportTime(q.Get("from"), loc)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid from: use RFC3339 or YYYY-MM-DD")
	}
	to, hasTo, err := parseReportTime(q.Get("to"), loc)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid to: use RFC3339 or YYYY-MM-DD")
	}
	if !hasTo {
		local := now.In(loc)
		to = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	}
	if !hasFrom {
		from = to.In(loc).AddDate(0, 0, -reportDefaultDays)
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	if to.Sub(from) > reportMaxDays*24*time.Hour {
		return time.Time{}, time.Time{}, errors.New("range must not exceed 366 days")
	}
	return from, to, nil
}

func parseReportTime(raw string, loc *time.Location) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, true, nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, raw, loc)
	if err != nil {
		return time.Time{}, false, err
	}
	return parsed, true, nil
}

// ratio returns num/den rounded to 4 decimals, or 0 when den is 0.
func ratio(num, den int64) float64 {
	if den == 0 {
		return 0
	}
	return math.Round(float64(num)/float64(den)*10000) / 10000
}

func reportQueryFailed(ctx context.Context, w http.ResponseWriter, report string, err error) {
	zerolog.Ctx(ctx).Error().Err(err).Str("report", report).Msg("admin report query failed")
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load report", nil)
}

type categoryReportRowDTO struct {
	CategoryID   *string `json:"categoryId"`
	CategoryName string  `json:"categoryName"`
	CategorySlug *string `json:"categorySlug"`
	UnitsSold    int64   `json:"unitsSold"`
	Orders       int64   `json:"orders"`
	Revenue      int64   `json:"revenue"`
	Share        float64 `json:"share"`
}

type categoryReportDTO struct {
	From         string                 `json:"from"`
	To           string                 `json:"to"`
	Timezone     string                 `json:"timezone"`
	TotalRevenue int64                  `json:"totalRevenue"`
	TotalUnits   int64                  `json:"totalUnits"`
	Rows         []categoryReportRowDTO `json:"rows"`
}

func buildCategoryReport(s reportScope, rows []dbgen.AdminReportSalesByCategoryRow) categoryReportDTO {
	out := categoryReportDTO{From: s.fromString(), To: s.toString(), Timezone: s.loc.String(), Rows: make([]categoryReportRowDTO, 0, len(rows))}
	for _, row := range rows {
		out.TotalRevenue += row.Revenue
		out.TotalUnits += row.UnitsSold
	}
	for _, row := range rows {
		out.Rows = append(out.Rows, categoryReportRowDTO{
			CategoryID:   nullableUUID(row.CategoryID),
			CategoryName: row.CategoryName,
			CategorySlug: nullableString(row.CategorySlug),
			UnitsSold:    row.UnitsSold,
			Orders:       row.Orders,
			Revenue:      row.Revenue,
			Share:        ratio(row.Revenue, out.TotalRevenue),
		})
	}
	return out
}

// SalesByCategory handles GET /api/v1/admin/reports/sales-by-category.
func (h *ReportsHandler) SalesByCategory(w http.ResponseWriter, r *http.Request) {
	s, csvOut, ok := h.scope(w, r)
	if !ok {
		return
	}
	rows, err := h.Q.AdminReportSalesByCategory(r.Context(), dbgen.AdminReportSalesByCategoryParams{TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end()})
	if err != nil {
		reportQueryFailed(r.Context(), w, "sales-by-category", err)
		return
	}
	report := buildCategoryReport(s, rows)
	if csvOut {
		writeCategoryCSV(r.Context(), w, s, report)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": report})
}

type productReportRowDTO struct {
	ProductID    *string `json:"productId"`
	Title        string  `json:"title"`
	Slug         string  `json:"slug"`
	CategoryID   *string `json:"categoryId"`
	CategoryName string  `json:"categoryName"`
	UnitsSold    int64   `json:"unitsSold"`
	Orders       int64   `json:"orders"`
	Revenue      int64   `json:"revenue"`
}

func productRow(row dbgen.AdminReportSalesByProductRow) productReportRowDTO {
	return productReportRowDTO{
		ProductID:    nullableUUID(row.ProductID),
		Title:        row.Title,
		Slug:         row.Slug,
		CategoryID:   nullableUUID(row.CategoryID),
		CategoryName: row.CategoryName,
		UnitsSold:    row.UnitsSold,
		Orders:       row.Orders,
		Revenue:      row.Revenue,
	}
}

// SalesByProduct handles GET /api/v1/admin/reports/sales-by-product.
func (h *ReportsHandler) SalesByProduct(w http.ResponseWriter, r *http.Request) {
	var categoryID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("categoryId")); raw != "" {
		parsed, err := parsePGUUID(raw)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid categoryId", nil)
			return
		}
		categoryID = parsed
	}
	s, csvOut, ok := h.scope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if csvOut {
		h.writeProductCSV(ctx, w, s, categoryID)
		return
	}
	page, limit, offset := parseListQuery(r)
	total, err := h.Q.AdminReportSalesByProductCount(ctx, dbgen.AdminReportSalesByProductCountParams{
		TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end(), CategoryID: categoryID,
	})
	if err != nil {
		reportQueryFailed(ctx, w, "sales-by-product", err)
		return
	}
	rows, err := h.Q.AdminReportSalesByProduct(ctx, dbgen.AdminReportSalesByProductParams{
		TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end(), CategoryID: categoryID,
		OffsetValue: int32(offset), LimitValue: int32(limit),
	})
	if err != nil {
		reportQueryFailed(ctx, w, "sales-by-product", err)
		return
	}
	data := make([]productReportRowDTO, 0, len(rows))
	for _, row := range rows {
		data = append(data, productRow(row))
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data":       data,
		"pagination": common.Pagination{Page: page, PerPage: limit, TotalItems: int(total)},
		"range":      s.rangeDTO(),
	})
}

type conversionReportDTO struct {
	From             string  `json:"from"`
	To               string  `json:"to"`
	Timezone         string  `json:"timezone"`
	CartsCreated     int64   `json:"cartsCreated"`
	CheckoutsStarted int64   `json:"checkoutsStarted"`
	PaidOrders       int64   `json:"paidOrders"`
	PendingOrders    int64   `json:"pendingOrders"`
	CancelledOrders  int64   `json:"cancelledOrders"`
	RefundedOrders   int64   `json:"refundedOrders"`
	PaidRevenue      int64   `json:"paidRevenue"`
	RefundedAmount   int64   `json:"refundedAmount"`
	CheckoutRate     float64 `json:"checkoutRate"`
	PaymentRate      float64 `json:"paymentRate"`
	ConversionRate   float64 `json:"conversionRate"`
}

func buildConversionReport(s reportScope, row dbgen.AdminReportConversionRow) conversionReportDTO {
	return conversionReportDTO{
		From:             s.fromString(),
		To:               s.toString(),
		Timezone:         s.loc.String(),
		CartsCreated:     row.CartsCreated,
		CheckoutsStarted: row.CheckoutsStarted,
		PaidOrders:       row.PaidOrders,
		PendingOrders:    row.PendingOrders,
		CancelledOrders:  row.CancelledOrders,
		RefundedOrders:   row.RefundedOrders,
		PaidRevenue:      row.PaidRevenue,
		RefundedAmount:   row.RefundedAmount,
		CheckoutRate:     ratio(row.CheckoutsStarted, row.CartsCreated),
		PaymentRate:      ratio(row.PaidOrders, row.CheckoutsStarted),
		ConversionRate:   ratio(row.PaidOrders, row.CartsCreated),
	}
}

// Conversion handles GET /api/v1/admin/reports/conversion.
func (h *ReportsHandler) Conversion(w http.ResponseWriter, r *http.Request) {
	s, csvOut, ok := h.scope(w, r)
	if !ok {
		return
	}
	row, err := h.Q.AdminReportConversion(r.Context(), dbgen.AdminReportConversionParams{TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end()})
	if err != nil {
		reportQueryFailed(r.Context(), w, "conversion", err)
		return
	}
	report := buildConversionReport(s, row)
	if csvOut {
		writeConversionCSV(r.Context(), w, s, report)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": report})
}

type customersDayDTO struct {
	Day                string `json:"day"`
	NewCustomers       int64  `json:"newCustomers"`
	ReturningCustomers int64  `json:"returningCustomers"`
	Orders             int64  `json:"orders"`
	Revenue            int64  `json:"revenue"`
}

type customersReportDTO struct {
	From                 string            `json:"from"`
	To                   string            `json:"to"`
	Timezone             string            `json:"timezone"`
	NewCustomers         int64             `json:"newCustomers"`
	ReturningCustomers   int64             `json:"returningCustomers"`
	TotalCustomers       int64             `json:"totalCustomers"`
	RepeatRate           float64           `json:"repeatRate"`
	OrdersFromNew        int64             `json:"ordersFromNew"`
	OrdersFromReturning  int64             `json:"ordersFromReturning"`
	RevenueFromNew       int64             `json:"revenueFromNew"`
	RevenueFromReturning int64             `json:"revenueFromReturning"`
	GuestOrders          int64             `json:"guestOrders"`
	GuestRevenue         int64             `json:"guestRevenue"`
	Daily                []customersDayDTO `json:"daily"`
}

func dateString(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

func buildCustomersReport(s reportScope, sum dbgen.AdminReportCustomersSummaryRow, daily []dbgen.AdminReportCustomersDailyRow) customersReportDTO {
	total := sum.NewCustomers + sum.ReturningCustomers
	out := customersReportDTO{
		From:                 s.fromString(),
		To:                   s.toString(),
		Timezone:             s.loc.String(),
		NewCustomers:         sum.NewCustomers,
		ReturningCustomers:   sum.ReturningCustomers,
		TotalCustomers:       total,
		RepeatRate:           ratio(sum.ReturningCustomers, total),
		OrdersFromNew:        sum.OrdersFromNew,
		OrdersFromReturning:  sum.OrdersFromReturning,
		RevenueFromNew:       sum.RevenueFromNew,
		RevenueFromReturning: sum.RevenueFromReturning,
		GuestOrders:          sum.GuestOrders,
		GuestRevenue:         sum.GuestRevenue,
		Daily:                make([]customersDayDTO, 0, len(daily)),
	}
	for _, row := range daily {
		out.Daily = append(out.Daily, customersDayDTO{
			Day:                dateString(row.Day),
			NewCustomers:       row.NewCustomers,
			ReturningCustomers: row.ReturningCustomers,
			Orders:             row.Orders,
			Revenue:            row.Revenue,
		})
	}
	return out
}

// Customers handles GET /api/v1/admin/reports/customers.
func (h *ReportsHandler) Customers(w http.ResponseWriter, r *http.Request) {
	s, csvOut, ok := h.scope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	daily, err := h.Q.AdminReportCustomersDaily(ctx, dbgen.AdminReportCustomersDailyParams{Tz: s.loc.String(), TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end()})
	if err != nil {
		reportQueryFailed(ctx, w, "customers", err)
		return
	}
	if csvOut {
		writeCustomersCSV(ctx, w, s, daily)
		return
	}
	sum, err := h.Q.AdminReportCustomersSummary(ctx, dbgen.AdminReportCustomersSummaryParams{TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end()})
	if err != nil {
		reportQueryFailed(ctx, w, "customers", err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": buildCustomersReport(s, sum, daily)})
}
