package admin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type stubReports struct {
	timezone string
	tzErr    error

	categories []dbgen.AdminReportSalesByCategoryRow
	categoryP  []dbgen.AdminReportSalesByCategoryParams

	products      func(arg dbgen.AdminReportSalesByProductParams) []dbgen.AdminReportSalesByProductRow
	productErrAt  int // fail the n-th product call (1-based); 0 = never
	productP      []dbgen.AdminReportSalesByProductParams
	productCount  int64
	productCountP []dbgen.AdminReportSalesByProductCountParams

	conversion  dbgen.AdminReportConversionRow
	conversionP []dbgen.AdminReportConversionParams

	summary  dbgen.AdminReportCustomersSummaryRow
	summaryP []dbgen.AdminReportCustomersSummaryParams
	daily    []dbgen.AdminReportCustomersDailyRow
	dailyP   []dbgen.AdminReportCustomersDailyParams
}

func (s *stubReports) GetTenantTimezone(_ context.Context, _ pgtype.UUID) (string, error) {
	if s.tzErr != nil {
		return "", s.tzErr
	}
	return s.timezone, nil
}

func (s *stubReports) AdminReportSalesByCategory(_ context.Context, arg dbgen.AdminReportSalesByCategoryParams) ([]dbgen.AdminReportSalesByCategoryRow, error) {
	s.categoryP = append(s.categoryP, arg)
	return s.categories, nil
}

func (s *stubReports) AdminReportSalesByProduct(_ context.Context, arg dbgen.AdminReportSalesByProductParams) ([]dbgen.AdminReportSalesByProductRow, error) {
	s.productP = append(s.productP, arg)
	if s.productErrAt > 0 && len(s.productP) == s.productErrAt {
		return nil, errors.New("boom")
	}
	if s.products == nil {
		return nil, nil
	}
	return s.products(arg), nil
}

func (s *stubReports) AdminReportSalesByProductCount(_ context.Context, arg dbgen.AdminReportSalesByProductCountParams) (int64, error) {
	s.productCountP = append(s.productCountP, arg)
	return s.productCount, nil
}

func (s *stubReports) AdminReportConversion(_ context.Context, arg dbgen.AdminReportConversionParams) (dbgen.AdminReportConversionRow, error) {
	s.conversionP = append(s.conversionP, arg)
	return s.conversion, nil
}

func (s *stubReports) AdminReportCustomersSummary(_ context.Context, arg dbgen.AdminReportCustomersSummaryParams) (dbgen.AdminReportCustomersSummaryRow, error) {
	s.summaryP = append(s.summaryP, arg)
	return s.summary, nil
}

func (s *stubReports) AdminReportCustomersDaily(_ context.Context, arg dbgen.AdminReportCustomersDailyParams) ([]dbgen.AdminReportCustomersDailyRow, error) {
	s.dailyP = append(s.dailyP, arg)
	return s.daily, nil
}

var reportNow = time.Date(2026, 10, 10, 20, 0, 0, 0, time.UTC) // 2026-10-11 03:00 WIB

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	id, err := parsePGUUID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func serveReport(h http.HandlerFunc, target, tenantID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if tenantID != "" {
		req = req.WithContext(tenant.With(req.Context(), tenantID))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rec, &body)
	return body.Error.Code
}

func TestParseReportRange(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	parse := func(query string) (time.Time, time.Time, error) {
		return parseReportRange(httptest.NewRequest("GET", "/r"+query, nil), reportNow, jakarta)
	}

	from, to, err := parse("")
	if err != nil {
		t.Fatal(err)
	}
	wantTo := time.Date(2026, 10, 12, 0, 0, 0, 0, jakarta)
	if !to.Equal(wantTo) || !from.Equal(wantTo.AddDate(0, 0, -30)) {
		t.Fatalf("defaults = [%s, %s), want [%s, %s)", from, to, wantTo.AddDate(0, 0, -30), wantTo)
	}
	if from.In(jakarta).Hour() != 0 {
		t.Fatalf("default from must be a local midnight, got %s", from.In(jakarta))
	}

	from, to, err = parse("?from=2026-10-01&to=2026-10-03")
	if err != nil {
		t.Fatal(err)
	}
	if got := from.UTC().Format(time.RFC3339); got != "2026-09-30T17:00:00Z" {
		t.Fatalf("date-only from = %s", got)
	}
	if got := to.UTC().Format(time.RFC3339); got != "2026-10-02T17:00:00Z" {
		t.Fatalf("date-only to = %s", got)
	}

	from, _, err = parse("?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z")
	if err != nil || !from.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC3339 must be used as-is: %s %v", from, err)
	}

	for _, bad := range []string{
		"?from=2026-10-03&to=2026-10-03",
		"?from=2026-10-04&to=2026-10-03",
		"?from=2025-01-01&to=2026-10-03",
		"?from=yesterday",
		"?to=2026-13-01",
	} {
		if _, _, err := parse(bad); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
	if _, _, err := parse("?from=2025-10-02&to=2026-10-03"); err != nil {
		t.Errorf("366 days must be accepted: %v", err)
	}
}

func TestSalesByCategoryJSON(t *testing.T) {
	tenantID := uuid.NewString()
	catID := uuid.NewString()
	stub := &stubReports{timezone: "Asia/Jakarta", categories: []dbgen.AdminReportSalesByCategoryRow{
		{CategoryID: mustUUID(t, catID), CategoryName: "Laptop", CategorySlug: pgtype.Text{String: "laptop", Valid: true}, UnitsSold: 3, Orders: 2, Revenue: 300000},
		{CategoryName: "Uncategorized", UnitsSold: 2, Orders: 2, Revenue: 200000},
	}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByCategory, "/r?from=2026-10-01&to=2026-10-03", tenantID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data categoryReportDTO `json:"data"`
	}
	decodeJSON(t, rec, &body)
	d := body.Data
	if d.Timezone != "Asia/Jakarta" || d.From != "2026-10-01T00:00:00+07:00" || d.To != "2026-10-03T00:00:00+07:00" {
		t.Fatalf("range echo wrong: %+v", d)
	}
	if d.TotalRevenue != 500000 || d.TotalUnits != 5 || len(d.Rows) != 2 {
		t.Fatalf("totals wrong: %+v", d)
	}
	if d.Rows[0].Share != 0.6 || d.Rows[1].Share != 0.4 {
		t.Fatalf("shares wrong: %+v", d.Rows)
	}
	if d.Rows[0].CategoryID == nil || *d.Rows[0].CategoryID != catID || d.Rows[1].CategoryID != nil || d.Rows[1].CategorySlug != nil {
		t.Fatalf("nullable category fields wrong: %+v", d.Rows)
	}
	if !strings.Contains(rec.Body.String(), `"categoryId":null`) {
		t.Fatalf("uncategorized categoryId must serialise as null: %s", rec.Body.String())
	}
	arg := stub.categoryP[0]
	if uuidString(arg.TenantID) != tenantID {
		t.Fatalf("tenant not passed: %s", uuidString(arg.TenantID))
	}
	if arg.StartAt.Time.UTC().Format(time.RFC3339) != "2026-09-30T17:00:00Z" || arg.EndAt.Time.UTC().Format(time.RFC3339) != "2026-10-02T17:00:00Z" {
		t.Fatalf("range not passed: %+v", arg)
	}
}

func TestSalesByCategoryEmptyRowsIsArray(t *testing.T) {
	h := &ReportsHandler{Q: &stubReports{timezone: "UTC"}, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByCategory, "/r", uuid.NewString())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"rows":[]`) {
		t.Fatalf("expected empty rows array, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSalesByProductPaginates(t *testing.T) {
	catID := uuid.NewString()
	stub := &stubReports{timezone: "UTC", productCount: 37, products: func(arg dbgen.AdminReportSalesByProductParams) []dbgen.AdminReportSalesByProductRow {
		return []dbgen.AdminReportSalesByProductRow{{ProductID: mustUUID(t, uuid.NewString()), Title: "MacBook", Slug: "macbook", CategoryName: "Uncategorized", UnitsSold: 1, Orders: 1, Revenue: 10}}
	}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByProduct, "/r?page=2&limit=10&categoryId="+catID, uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	arg := stub.productP[0]
	if arg.OffsetValue != 10 || arg.LimitValue != 10 || uuidString(arg.CategoryID) != catID || uuidString(stub.productCountP[0].CategoryID) != catID {
		t.Fatalf("unexpected args: %+v", arg)
	}
	var body struct {
		Data       []productReportRowDTO `json:"data"`
		Pagination struct {
			Page       int `json:"page"`
			PerPage    int `json:"perPage"`
			TotalItems int `json:"totalItems"`
		} `json:"pagination"`
		Range reportRangeDTO `json:"range"`
	}
	decodeJSON(t, rec, &body)
	if body.Pagination.Page != 2 || body.Pagination.PerPage != 10 || body.Pagination.TotalItems != 37 || len(body.Data) != 1 || body.Range.Timezone != "UTC" {
		t.Fatalf("unexpected body: %+v", body)
	}

	rec = serveReport(h.SalesByProduct, "/r?categoryId=nope", uuid.NewString())
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "BAD_REQUEST" {
		t.Fatalf("expected 400 for bad categoryId, got %d", rec.Code)
	}
}

func TestConversionRates(t *testing.T) {
	stub := &stubReports{timezone: "UTC"}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.Conversion, "/r", uuid.NewString())
	var body struct {
		Data conversionReportDTO `json:"data"`
	}
	decodeJSON(t, rec, &body)
	if rec.Code != http.StatusOK || body.Data.CheckoutRate != 0 || body.Data.PaymentRate != 0 || body.Data.ConversionRate != 0 {
		t.Fatalf("zero denominators must give 0: %d %+v", rec.Code, body.Data)
	}

	stub.conversion = dbgen.AdminReportConversionRow{CartsCreated: 3, CheckoutsStarted: 7, PaidOrders: 4, PendingOrders: 1, CancelledOrders: 1, RefundedOrders: 1, PaidRevenue: 495000, RefundedAmount: 1004000}
	rec = serveReport(h.Conversion, "/r", uuid.NewString())
	decodeJSON(t, rec, &body)
	d := body.Data
	if d.CheckoutRate != 2.3333 || d.PaymentRate != 0.5714 || d.ConversionRate != 1.3333 {
		t.Fatalf("rates wrong: %+v", d)
	}
	if d.RefundedOrders != 1 || d.RefundedAmount != 1004000 || d.PaidRevenue != 495000 {
		t.Fatalf("pass-through wrong: %+v", d)
	}
}

func TestCustomersSummaryAndDaily(t *testing.T) {
	stub := &stubReports{
		timezone: "Asia/Jakarta",
		summary:  dbgen.AdminReportCustomersSummaryRow{NewCustomers: 3, ReturningCustomers: 1, OrdersFromNew: 3, OrdersFromReturning: 2, RevenueFromNew: 300, RevenueFromReturning: 200, GuestOrders: 1, GuestRevenue: 50},
		daily: []dbgen.AdminReportCustomersDailyRow{
			{Day: pgtype.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Valid: true}, NewCustomers: 2, Orders: 2, Revenue: 300},
		},
	}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.Customers, "/r?from=2026-10-01&to=2026-10-03", uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data customersReportDTO `json:"data"`
	}
	decodeJSON(t, rec, &body)
	d := body.Data
	if d.TotalCustomers != 4 || d.RepeatRate != 0.25 || d.GuestOrders != 1 || d.GuestRevenue != 50 {
		t.Fatalf("summary wrong: %+v", d)
	}
	if len(d.Daily) != 1 || d.Daily[0].Day != "2026-10-01" || d.Daily[0].NewCustomers != 2 {
		t.Fatalf("daily wrong: %+v", d.Daily)
	}
	if stub.dailyP[0].Tz != "Asia/Jakarta" {
		t.Fatalf("tz not passed: %q", stub.dailyP[0].Tz)
	}
}

func TestReportsRequireTenant(t *testing.T) {
	h := &ReportsHandler{Q: &stubReports{timezone: "UTC"}, Now: func() time.Time { return reportNow }}
	for name, fn := range map[string]http.HandlerFunc{"category": h.SalesByCategory, "product": h.SalesByProduct, "conversion": h.Conversion, "customers": h.Customers} {
		rec := serveReport(fn, "/r", "")
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "TENANT_REQUIRED" {
			t.Fatalf("%s: expected 400 TENANT_REQUIRED, got %d", name, rec.Code)
		}
	}
	h = &ReportsHandler{Q: &stubReports{tzErr: pgx.ErrNoRows}, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.Conversion, "/r", uuid.NewString())
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "TENANT_REQUIRED" {
		t.Fatalf("unknown tenant: expected 400 TENANT_REQUIRED, got %d", rec.Code)
	}
	h = &ReportsHandler{Q: &stubReports{tzErr: errors.New("db down")}, Now: func() time.Time { return reportNow }}
	rec = serveReport(h.Conversion, "/r", uuid.NewString())
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("db error: expected 500, got %d", rec.Code)
	}
}

func TestReportsRejectBadFormatAndRange(t *testing.T) {
	h := &ReportsHandler{Q: &stubReports{timezone: "UTC"}, Now: func() time.Time { return reportNow }}
	for _, target := range []string{"/r?format=xlsx", "/r?from=2026-10-05&to=2026-10-01"} {
		rec := serveReport(h.Conversion, target, uuid.NewString())
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", target, rec.Code)
		}
	}
}

func TestReportsInvalidTimezoneFallsBackToUTC(t *testing.T) {
	h := &ReportsHandler{Q: &stubReports{timezone: "Mars/Phobos"}, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.Conversion, "/r", uuid.NewString())
	var body struct {
		Data conversionReportDTO `json:"data"`
	}
	decodeJSON(t, rec, &body)
	if rec.Code != http.StatusOK || body.Data.Timezone != "UTC" {
		t.Fatalf("expected UTC fallback, got %d %+v", rec.Code, body.Data)
	}
}

// ---- CSV ----

func readCSV(t *testing.T, rec *httptest.ResponseRecorder) [][]string {
	t.Helper()
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatalf("CSV must start with a UTF-8 BOM: %q", body[:min(len(body), 10)])
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return records
}

func TestCSVCellSanitizes(t *testing.T) {
	for _, in := range []string{"=1+1", "+1", "-1", "@SUM(A1)", "\tx", "\rx", `=HYPERLINK("http://x","y")`} {
		if got := csvCell(in); got != "'"+in {
			t.Errorf("csvCell(%q) = %q, want leading quote", in, got)
		}
	}
	for _, in := range []string{"Sạc & Cáp", "123", "", "Laptop =x", "'quoted"} {
		if got := csvCell(in); got != in {
			t.Errorf("csvCell(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestSalesByCategoryCSV(t *testing.T) {
	stub := &stubReports{timezone: "Asia/Jakarta", categories: []dbgen.AdminReportSalesByCategoryRow{
		{CategoryName: `=HYPERLINK("http://x","y")`, CategorySlug: pgtype.Text{String: "-slug", Valid: true}, UnitsSold: 3, Orders: 2, Revenue: 300000},
		{CategoryName: "Sạc & Cáp, \"USB\"", UnitsSold: 1, Orders: 1, Revenue: 200000},
	}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByCategory, "/r?from=2026-10-01&to=2026-10-03&format=csv", uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Fatalf("content-type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="sales-by-category_2026-10-01_2026-10-03.csv"` {
		t.Fatalf("content-disposition %q", cd)
	}
	records := readCSV(t, rec)
	if strings.Join(records[0], ",") != "category_id,category,category_slug,units_sold,orders,revenue,share" {
		t.Fatalf("header %v", records[0])
	}
	if len(records) != 3 {
		t.Fatalf("expected 2 data rows, got %d", len(records)-1)
	}
	want := []string{"", `'=HYPERLINK("http://x","y")`, "'-slug", "3", "2", "300000", "0.6"}
	if strings.Join(records[1], "|") != strings.Join(want, "|") {
		t.Fatalf("row 1 = %q, want %q", records[1], want)
	}
	if records[2][1] != "Sạc & Cáp, \"USB\"" {
		t.Fatalf("non-ASCII/quoted text must round-trip: %q", records[2][1])
	}
}

func TestSalesByProductCSVIteratesPages(t *testing.T) {
	stub := &stubReports{timezone: "UTC", products: func(arg dbgen.AdminReportSalesByProductParams) []dbgen.AdminReportSalesByProductRow {
		n := int(arg.LimitValue)
		if arg.OffsetValue >= 1000 {
			n = 3
		}
		rows := make([]dbgen.AdminReportSalesByProductRow, n)
		for i := range rows {
			rows[i] = dbgen.AdminReportSalesByProductRow{Title: "+P", Slug: "p", CategoryName: "Uncategorized", UnitsSold: 1, Orders: 1, Revenue: 1}
		}
		return rows
	}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByProduct, "/r?format=csv&page=5&limit=2", uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	records := readCSV(t, rec)
	if len(records) != 1004 {
		t.Fatalf("expected header + 1003 rows, got %d", len(records))
	}
	if len(stub.productP) != 2 || stub.productP[0].OffsetValue != 0 || stub.productP[0].LimitValue != 1000 || stub.productP[1].OffsetValue != 1000 {
		t.Fatalf("unexpected paging: %+v", stub.productP)
	}
	if records[1][1] != "'+P" {
		t.Fatalf("title must be sanitized: %q", records[1][1])
	}
	if len(stub.productCountP) != 0 {
		t.Fatal("CSV export must not run the count query")
	}
}

func TestSalesByProductCSVStopsAtCap(t *testing.T) {
	stub := &stubReports{timezone: "UTC", products: func(arg dbgen.AdminReportSalesByProductParams) []dbgen.AdminReportSalesByProductRow {
		return make([]dbgen.AdminReportSalesByProductRow, arg.LimitValue)
	}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByProduct, "/r?format=csv", uuid.NewString())
	if got := strings.Count(rec.Body.String(), "\n"); got != 1+csvProductMaxRows {
		t.Fatalf("expected header + %d rows, got %d lines", csvProductMaxRows, got)
	}
	if len(stub.productP) != csvProductMaxRows/csvProductPage {
		t.Fatalf("expected %d queries, got %d", csvProductMaxRows/csvProductPage, len(stub.productP))
	}
}

func TestSalesByProductCSVFirstPageErrorIsJSON(t *testing.T) {
	stub := &stubReports{timezone: "UTC", productErrAt: 1}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.SalesByProduct, "/r?format=csv", uuid.NewString())
	if rec.Code != http.StatusInternalServerError || errCode(t, rec) != "INTERNAL" {
		t.Fatalf("expected JSON 500, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestConversionCSV(t *testing.T) {
	stub := &stubReports{timezone: "UTC", conversion: dbgen.AdminReportConversionRow{CartsCreated: 4, CheckoutsStarted: 2, PaidOrders: 1, PaidRevenue: 100}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.Conversion, "/r?from=2026-10-01&to=2026-10-02&format=CSV", uuid.NewString())
	records := readCSV(t, rec)
	if len(records) != 2 || len(records[0]) != len(conversionCSVHeader) || len(records[1]) != len(conversionCSVHeader) {
		t.Fatalf("unexpected csv: %v", records)
	}
	if strings.Join(records[1][:6], ",") != "2026-10-01T00:00:00Z,2026-10-02T00:00:00Z,UTC,4,2,1" || records[1][11] != "0.5" {
		t.Fatalf("unexpected row: %v", records[1])
	}
}

func TestCustomersCSV(t *testing.T) {
	stub := &stubReports{timezone: "Asia/Jakarta", daily: []dbgen.AdminReportCustomersDailyRow{
		{Day: pgtype.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Valid: true}, NewCustomers: 2, Orders: 2, Revenue: 300000},
		{Day: pgtype.Date{Time: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Valid: true}, ReturningCustomers: 1, Orders: 1, Revenue: 150000},
	}}
	h := &ReportsHandler{Q: stub, Now: func() time.Time { return reportNow }}
	rec := serveReport(h.Customers, "/r?format=csv", uuid.NewString())
	records := readCSV(t, rec)
	if len(records) != 3 || strings.Join(records[0], ",") != "day,new_customers,returning_customers,orders,revenue" {
		t.Fatalf("unexpected csv: %v", records)
	}
	if strings.Join(records[2], ",") != "2026-10-02,0,1,1,150000" {
		t.Fatalf("unexpected row: %v", records[2])
	}
	if len(stub.summaryP) != 0 {
		t.Fatal("customers CSV is the daily series only")
	}
}
