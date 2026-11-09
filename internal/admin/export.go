package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	defaultProductExportBatch = 500
	defaultOrderExportBatch   = 1000
	exportFileTimeLayout      = "20060102-150405"
)

// ExportProductsCSV handles GET /api/v1/admin/products/export. It streams one
// row per variant (a product without variants yields one row with empty
// variant columns) for every product matching the admin list filters. Pages
// are cut per product so a product's variants never straddle two pages.
func (h *CatalogHandler) ExportProductsCSV(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	filters, _, err := parseProductListFilters(r)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	threshold, err := catalog.ResolveTenantLowStockThreshold(ctx, h.Q, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load inventory settings", nil)
		return
	}
	batch := h.ExportBatchSize
	if batch <= 0 {
		batch = defaultProductExportBatch
	}
	params := dbgen.AdminExportProductPageParams{
		TenantID:         tenantID,
		Search:           filters.Search,
		CategorySlug:     filters.Category,
		BrandSlug:        filters.Brand,
		InStock:          filters.InStock,
		Status:           filters.Status,
		StockStatus:      filters.StockStatus,
		DefaultThreshold: threshold,
		BatchSize:        int32(batch),
	}
	// The first page is fetched before any byte is written so filter or
	// database failures still produce a JSON error response.
	rows, err := h.Q.AdminExportProductPage(ctx, params)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to export products", nil)
		return
	}
	stream, err := newCSVStream(w, "products-"+time.Now().UTC().Format(exportFileTimeLayout)+".csv", productExportColumns)
	if err != nil {
		return
	}
	logger := zerolog.Ctx(ctx)
	for {
		products := 0
		var last dbgen.AdminExportProductPageRow
		for i, row := range rows {
			if i == 0 || row.ProductID != rows[i-1].ProductID {
				products++
			}
			last = row
			if err := stream.write(productExportRecord(row)); err != nil {
				logger.Error().Err(err).Msg("products export: write failed")
				return
			}
		}
		if err := stream.flush(); err != nil {
			logger.Error().Err(err).Msg("products export: flush failed")
			return
		}
		if products < batch {
			return
		}
		params.AfterCreatedAt = last.CreatedAt
		params.AfterID = last.ProductID
		rows, err = h.Q.AdminExportProductPage(ctx, params)
		if err != nil {
			// Headers are already sent: the file ends early. Documented in
			// openapi (clients compare the row count with the list total).
			logger.Error().Err(err).Msg("products export: page query failed, file truncated")
			return
		}
	}
}

func productExportRecord(row dbgen.AdminExportProductPageRow) []string {
	attributes := ""
	if row.VariantID.Valid {
		attributes = string(row.Attributes)
		if attributes == "" {
			attributes = "{}"
		}
	}
	stock := intCell4(row.Stock)
	return []string{
		csvSafeCell(row.Sku.String),
		uuidString(row.ProductID),
		uuidString(row.VariantID),
		csvSafeCell(row.Title),
		csvSafeCell(row.Slug),
		row.Status,
		strconv.FormatInt(row.Price, 10),
		intCell8(row.CompareAt),
		intCell8(row.VariantPrice),
		stock,
		stock, // stock_baseline: the stock at export time (see import STALE_STOCK)
		intCell4(row.LowStockThreshold),
		strconv.FormatBool(row.InStock),
		row.StockStatus,
		csvSafeCell(row.CategorySlug.String),
		csvSafeCell(row.BrandSlug.String),
		csvSafeCell(row.Description.String),
		csvSafeCell(attributes),
		formatCSVTime(row.CreatedAt),
		formatCSVTime(row.UpdatedAt),
	}
}

// ExportOrdersCSV handles GET /api/v1/admin/orders/export: one row per order
// with the same filters as the admin order list (status, search, startDate,
// endDate; dates are UTC, endDate exclusive).
func (h *OrdersHandler) ExportOrdersCSV(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	status, err := orderStatusFilter(r.URL.Query().Get("status"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid status filter", nil)
		return
	}
	startAt, err := queryTime(r, "startDate")
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	endAt, err := queryTime(r, "endDate")
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	batch := h.ExportBatchSize
	if batch <= 0 {
		batch = defaultOrderExportBatch
	}
	ctx := r.Context()
	params := dbgen.AdminExportOrderRowsParams{
		TenantID:  tenantIDFromContext(ctx),
		Status:    status,
		Search:    queryText(r, "search"),
		StartAt:   startAt,
		EndAt:     endAt,
		BatchSize: int32(batch),
	}
	rows, err := h.Q.AdminExportOrderRows(ctx, params)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to export orders", nil)
		return
	}
	stream, err := newCSVStream(w, "orders-"+time.Now().UTC().Format(exportFileTimeLayout)+".csv", orderExportColumns)
	if err != nil {
		return
	}
	logger := zerolog.Ctx(ctx)
	for {
		for _, row := range rows {
			if err := stream.write(orderExportRecord(row)); err != nil {
				logger.Error().Err(err).Msg("orders export: write failed")
				return
			}
		}
		if err := stream.flush(); err != nil {
			logger.Error().Err(err).Msg("orders export: flush failed")
			return
		}
		if len(rows) < batch {
			return
		}
		last := rows[len(rows)-1]
		params.AfterCreatedAt = last.CreatedAt
		params.AfterID = last.ID
		rows, err = h.Q.AdminExportOrderRows(ctx, params)
		if err != nil {
			logger.Error().Err(err).Msg("orders export: page query failed, file truncated")
			return
		}
	}
}

func orderExportRecord(row dbgen.AdminExportOrderRowsRow) []string {
	return []string{
		csvSafeCell(row.OrderNumber.String),
		uuidString(row.ID),
		string(row.Status),
		csvSafeCell(row.PaymentStatus),
		csvSafeCell(row.CustomerName.String),
		csvSafeCell(row.CustomerEmail.String),
		csvSafeCell(row.Currency),
		strconv.FormatInt(row.PricingSubtotal, 10),
		strconv.FormatInt(row.PricingDiscount, 10),
		strconv.FormatInt(row.PricingTax, 10),
		strconv.FormatInt(row.PricingShipping, 10),
		strconv.FormatInt(row.PricingTotal, 10),
		csvSafeCell(row.AppliedVoucherCode.String),
		strconv.FormatInt(int64(row.ItemsCount), 10),
		csvSafeCell(row.Courier.String),
		csvSafeCell(row.TrackingNumber.String),
		formatCSVTime(row.CreatedAt),
		formatCSVTime(row.UpdatedAt),
	}
}

func intCell8(v pgtype.Int8) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatInt(v.Int64, 10)
}

func intCell4(v pgtype.Int4) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatInt(int64(v.Int32), 10)
}

func formatCSVTime(v pgtype.Timestamptz) string {
	if !v.Valid {
		return ""
	}
	return v.Time.UTC().Format(timeLayout)
}
