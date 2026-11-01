package admin

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	csvFlushEvery     = 500
	csvProductPage    = 1000
	csvProductMaxRows = 100000
)

// csvCell neutralises spreadsheet formula injection: a text cell starting
// with = + - @ TAB or CR is prefixed with a single quote so spreadsheet apps
// show it as literal text. Only user-controlled text goes through it; numbers,
// UUIDs and dates are written raw.
func csvCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func csvInt(v int64) string { return strconv.FormatInt(v, 10) }

func csvFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func csvOptional(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// reportCSVStream writes an RFC 4180 CSV attachment (UTF-8 with BOM so Excel reads
// non-ASCII text correctly) and flushes to the client every csvFlushEvery rows
// when the writer chain supports it. Once the first byte is written the status
// is 200, so a later failure can only truncate the file; it is logged.
type reportCSVStream struct {
	ctx  context.Context
	rc   *http.ResponseController
	cw   *csv.Writer
	rows int
}

func reportFilename(report string, s reportScope) string {
	return fmt.Sprintf("%s_%s_%s.csv", report, s.from.In(s.loc).Format(time.DateOnly), s.to.In(s.loc).Format(time.DateOnly))
}

func startCSV(ctx context.Context, w http.ResponseWriter, filename string, header []string) *reportCSVStream {
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	// The filename is server-generated (report name and dates only).
	h.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("\xEF\xBB\xBF"))
	s := &reportCSVStream{ctx: ctx, rc: http.NewResponseController(w), cw: csv.NewWriter(w)}
	_ = s.cw.Write(header)
	return s
}

func (s *reportCSVStream) write(record []string) error {
	if err := s.cw.Write(record); err != nil {
		return err
	}
	s.rows++
	if s.rows%csvFlushEvery == 0 {
		s.cw.Flush()
		if err := s.cw.Error(); err != nil {
			return err
		}
		if err := s.rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
	}
	return nil
}

func (s *reportCSVStream) close() {
	s.cw.Flush()
	if err := s.cw.Error(); err != nil {
		zerolog.Ctx(s.ctx).Warn().Err(err).Msg("csv export: flush failed")
	}
}

func (s *reportCSVStream) abort(report string, err error) {
	s.cw.Flush()
	zerolog.Ctx(s.ctx).Error().Err(err).Str("report", report).Int("rows_written", s.rows).Msg("csv export truncated")
}

var categoryCSVHeader = []string{"category_id", "category", "category_slug", "units_sold", "orders", "revenue", "share"}

func writeCategoryCSV(ctx context.Context, w http.ResponseWriter, s reportScope, report categoryReportDTO) {
	out := startCSV(ctx, w, reportFilename("sales-by-category", s), categoryCSVHeader)
	for _, row := range report.Rows {
		if err := out.write([]string{
			csvOptional(row.CategoryID),
			csvCell(row.CategoryName),
			csvCell(csvOptional(row.CategorySlug)),
			csvInt(row.UnitsSold),
			csvInt(row.Orders),
			csvInt(row.Revenue),
			csvFloat(row.Share),
		}); err != nil {
			out.abort("sales-by-category", err)
			return
		}
	}
	out.close()
}

var productCSVHeader = []string{"product_id", "title", "slug", "category_id", "category", "units_sold", "orders", "revenue"}

func productCSVRecord(row dbgen.AdminReportSalesByProductRow) []string {
	dto := productRow(row)
	return []string{
		csvOptional(dto.ProductID),
		csvCell(dto.Title),
		csvCell(dto.Slug),
		csvOptional(dto.CategoryID),
		csvCell(dto.CategoryName),
		csvInt(dto.UnitsSold),
		csvInt(dto.Orders),
		csvInt(dto.Revenue),
	}
}

// writeProductCSV ignores page/limit and walks the whole result set in pages
// of csvProductPage rows, stopping at csvProductMaxRows. The first page is
// loaded before any byte is written, so a failing query still gets a JSON 500.
func (h *ReportsHandler) writeProductCSV(ctx context.Context, w http.ResponseWriter, s reportScope, categoryID pgtype.UUID) {
	fetch := func(offset int) ([]dbgen.AdminReportSalesByProductRow, error) {
		limit := csvProductPage
		if remaining := csvProductMaxRows - offset; remaining < limit {
			limit = remaining
		}
		return h.Q.AdminReportSalesByProduct(ctx, dbgen.AdminReportSalesByProductParams{
			TenantID: s.tenantID, StartAt: s.start(), EndAt: s.end(), CategoryID: categoryID,
			OffsetValue: int32(offset), LimitValue: int32(limit),
		})
	}
	page, err := fetch(0)
	if err != nil {
		reportQueryFailed(ctx, w, "sales-by-product", err)
		return
	}
	out := startCSV(ctx, w, reportFilename("sales-by-product", s), productCSVHeader)
	offset := 0
	for {
		for _, row := range page {
			if err := out.write(productCSVRecord(row)); err != nil {
				out.abort("sales-by-product", err)
				return
			}
		}
		offset += len(page)
		if len(page) < csvProductPage || offset >= csvProductMaxRows {
			break
		}
		if page, err = fetch(offset); err != nil {
			out.abort("sales-by-product", err)
			return
		}
	}
	out.close()
}

var conversionCSVHeader = []string{"from", "to", "timezone", "carts_created", "checkouts_started", "paid_orders", "pending_orders", "cancelled_orders", "refunded_orders", "paid_revenue", "refunded_amount", "checkout_rate", "payment_rate", "conversion_rate"}

func writeConversionCSV(ctx context.Context, w http.ResponseWriter, s reportScope, c conversionReportDTO) {
	out := startCSV(ctx, w, reportFilename("conversion", s), conversionCSVHeader)
	if err := out.write([]string{
		c.From, c.To, c.Timezone,
		csvInt(c.CartsCreated), csvInt(c.CheckoutsStarted), csvInt(c.PaidOrders), csvInt(c.PendingOrders),
		csvInt(c.CancelledOrders), csvInt(c.RefundedOrders), csvInt(c.PaidRevenue), csvInt(c.RefundedAmount),
		csvFloat(c.CheckoutRate), csvFloat(c.PaymentRate), csvFloat(c.ConversionRate),
	}); err != nil {
		out.abort("conversion", err)
		return
	}
	out.close()
}

var customersCSVHeader = []string{"day", "new_customers", "returning_customers", "orders", "revenue"}

func writeCustomersCSV(ctx context.Context, w http.ResponseWriter, s reportScope, daily []dbgen.AdminReportCustomersDailyRow) {
	out := startCSV(ctx, w, reportFilename("customers", s), customersCSVHeader)
	for _, row := range daily {
		if err := out.write([]string{
			dateString(row.Day),
			csvInt(row.NewCustomers),
			csvInt(row.ReturningCustomers),
			csvInt(row.Orders),
			csvInt(row.Revenue),
		}); err != nil {
			out.abort("customers", err)
			return
		}
	}
	out.close()
}
