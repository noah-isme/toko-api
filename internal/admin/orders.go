package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// OrdersHandler serves admin order list/detail/stats reads. Order mutations
// (status transitions, shipment creation) stay in internal/orders because they
// carry inventory and payment side effects.
type OrdersHandler struct {
	Q dbgen.Querier
}

type orderSummaryDTO struct {
	ID            string  `json:"id"`
	OrderNumber   *string `json:"orderNumber,omitempty"`
	UserID        *string `json:"userId,omitempty"`
	CustomerName  *string `json:"customerName,omitempty"`
	CustomerEmail *string `json:"customerEmail,omitempty"`
	Status        string  `json:"status"`
	PaymentStatus string  `json:"paymentStatus"`
	Currency      string  `json:"currency"`
	Total         int64   `json:"total"`
	Subtotal      int64   `json:"subtotal"`
	Discount      int64   `json:"discount"`
	Tax           int64   `json:"tax"`
	Shipping      int64   `json:"shipping"`
	VoucherCode   *string `json:"voucherCode,omitempty"`
	ItemsCount    int32   `json:"itemsCount"`
	Courier       *string `json:"courier,omitempty"`
	Tracking      *string `json:"trackingNumber,omitempty"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
}

type orderItemDTO struct {
	ID        string  `json:"id"`
	ProductID *string `json:"productId,omitempty"`
	VariantID *string `json:"variantId,omitempty"`
	Title     string  `json:"title"`
	Slug      string  `json:"slug"`
	Qty       int32   `json:"qty"`
	UnitPrice int64   `json:"unitPrice"`
	Subtotal  int64   `json:"subtotal"`
}

type orderDetailDTO struct {
	orderSummaryDTO
	ShippingAddress json.RawMessage `json:"shippingAddress"`
	ShippingOption  json.RawMessage `json:"shippingOption"`
	Notes           *string         `json:"notes,omitempty"`
	Items           []orderItemDTO  `json:"items"`
}

// ListOrders handles GET /api/v1/admin/orders.
func (h *OrdersHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	page, limit, offset := parseListQuery(r)
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
	search := queryText(r, "search")

	ctx := r.Context()
	total, err := h.Q.AdminCountOrders(ctx, dbgen.AdminCountOrdersParams{
		Status:  status,
		Search:  search,
		StartAt: startAt,
		EndAt:   endAt,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count orders", nil)
		return
	}
	rows, err := h.Q.AdminListOrders(ctx, dbgen.AdminListOrdersParams{
		Status:      status,
		Search:      search,
		StartAt:     startAt,
		EndAt:       endAt,
		OffsetValue: int32(offset),
		LimitValue:  int32(limit),
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list orders", nil)
		return
	}
	items := make([]orderSummaryDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, orderSummaryDTO{
			ID:            uuidString(row.ID),
			OrderNumber:   nullableString(row.OrderNumber),
			UserID:        nullableUUID(row.UserID),
			CustomerName:  nullableString(row.CustomerName),
			CustomerEmail: nullableString(row.CustomerEmail),
			Status:        string(row.Status),
			PaymentStatus: row.PaymentStatus,
			Currency:      row.Currency,
			Total:         row.PricingTotal,
			Subtotal:      row.PricingSubtotal,
			Discount:      row.PricingDiscount,
			Tax:           row.PricingTax,
			Shipping:      row.PricingShipping,
			VoucherCode:   nullableString(row.AppliedVoucherCode),
			ItemsCount:    row.ItemsCount,
			Courier:       nullableString(row.Courier),
			Tracking:      nullableString(row.TrackingNumber),
			CreatedAt:     timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:     timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		})
	}
	writePaginated(w, items, page, limit, total)
}

// GetOrder handles GET /api/v1/admin/orders/{id}.
func (h *OrdersHandler) GetOrder(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	id, err := parsePGUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	ctx := r.Context()
	row, err := h.Q.AdminGetOrder(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order", nil)
		return
	}
	orderItems, err := h.Q.ListOrderItemsByOrder(ctx, id)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order items", nil)
		return
	}
	detail := orderDetailDTO{
		orderSummaryDTO: orderSummaryDTO{
			ID:            uuidString(row.ID),
			OrderNumber:   nullableString(row.OrderNumber),
			UserID:        nullableUUID(row.UserID),
			CustomerName:  nullableString(row.CustomerName),
			CustomerEmail: nullableString(row.CustomerEmail),
			Status:        string(row.Status),
			PaymentStatus: row.PaymentStatus,
			Currency:      row.Currency,
			Total:         row.PricingTotal,
			Subtotal:      row.PricingSubtotal,
			Discount:      row.PricingDiscount,
			Tax:           row.PricingTax,
			Shipping:      row.PricingShipping,
			VoucherCode:   nullableString(row.AppliedVoucherCode),
			ItemsCount:    row.ItemsCount,
			Courier:       nullableString(row.Courier),
			Tracking:      nullableString(row.TrackingNumber),
			CreatedAt:     timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:     timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		},
		ShippingAddress: rawJSON(row.ShippingAddress),
		ShippingOption:  rawJSON(row.ShippingOption),
		Notes:           nullableString(row.Notes),
		Items:           make([]orderItemDTO, 0, len(orderItems)),
	}
	for _, item := range orderItems {
		detail.Items = append(detail.Items, orderItemDTO{
			ID:        uuidString(item.ID),
			ProductID: nullableUUID(item.ProductID),
			VariantID: nullableUUID(item.VariantID),
			Title:     item.Title,
			Slug:      item.Slug,
			Qty:       item.Qty,
			UnitPrice: item.UnitPrice,
			Subtotal:  item.Subtotal,
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": detail})
}

type orderStatsDTO struct {
	TotalOrders       int64 `json:"totalOrders"`
	TotalRevenue      int64 `json:"totalRevenue"`
	PendingOrders     int64 `json:"pendingOrders"`
	PaidOrders        int64 `json:"paidOrders"`
	ShippedOrders     int64 `json:"shippedOrders"`
	DeliveredOrders   int64 `json:"deliveredOrders"`
	CancelledOrders   int64 `json:"cancelledOrders"`
	AverageOrderValue int64 `json:"averageOrderValue"`
}

// OrderStats handles GET /api/v1/admin/orders/stats.
func (h *OrdersHandler) OrderStats(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
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
	stats, err := h.Q.AdminOrderStats(r.Context(), dbgen.AdminOrderStatsParams{StartAt: startAt, EndAt: endAt})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order stats", nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": orderStatsDTO{
		TotalOrders:       stats.TotalOrders,
		TotalRevenue:      stats.TotalRevenue,
		PendingOrders:     stats.PendingOrders,
		PaidOrders:        stats.PaidOrders,
		ShippedOrders:     stats.ShippedOrders,
		DeliveredOrders:   stats.DeliveredOrders,
		CancelledOrders:   stats.CancelledOrders,
		AverageOrderValue: stats.AverageOrderValue,
	}})
}

type voucherDTO struct {
	ID           string   `json:"id"`
	Code         string   `json:"code"`
	Kind         string   `json:"kind"`
	Value        int64    `json:"value"`
	PercentBps   *int32   `json:"percentBps,omitempty"`
	MinSpend     int64    `json:"minSpend"`
	UsageLimit   *int32   `json:"usageLimit,omitempty"`
	UsedCount    int32    `json:"usedCount"`
	PerUserLimit *int32   `json:"perUserLimit,omitempty"`
	ValidFrom    *string  `json:"validFrom,omitempty"`
	ValidTo      *string  `json:"validTo,omitempty"`
	Combinable   bool     `json:"combinable"`
	Priority     int32    `json:"priority"`
	ProductIDs   []string `json:"productIds"`
	CategoryIDs  []string `json:"categoryIds"`
	BrandIDs     []string `json:"brandIds"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

// ListVouchers handles GET /api/v1/admin/vouchers.
func (h *OrdersHandler) ListVouchers(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	page, limit, offset := parseListQuery(r)
	kind, err := discountKindFilter(r.URL.Query().Get("kind"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid kind filter", nil)
		return
	}
	search := queryText(r, "search")

	ctx := r.Context()
	total, err := h.Q.AdminCountVouchers(ctx, dbgen.AdminCountVouchersParams{Search: search, Kind: kind})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count vouchers", nil)
		return
	}
	rows, err := h.Q.AdminListVouchers(ctx, dbgen.AdminListVouchersParams{
		Search:      search,
		Kind:        kind,
		OffsetValue: int32(offset),
		LimitValue:  int32(limit),
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list vouchers", nil)
		return
	}
	items := make([]voucherDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, voucherDTO{
			ID:           uuidString(row.ID),
			Code:         row.Code,
			Kind:         string(row.Kind),
			Value:        row.Value,
			PercentBps:   nullableInt32(row.PercentBps),
			MinSpend:     row.MinSpend,
			UsageLimit:   nullableInt32(row.UsageLimit),
			UsedCount:    row.UsedCount,
			PerUserLimit: nullableInt32(row.PerUserLimit),
			ValidFrom:    formatNullableTime(row.ValidFrom),
			ValidTo:      formatNullableTime(row.ValidTo),
			Combinable:   row.Combinable,
			Priority:     row.Priority,
			ProductIDs:   uuidStrings(row.ProductIds),
			CategoryIDs:  uuidStrings(row.CategoryIds),
			BrandIDs:     uuidStrings(row.BrandIds),
			CreatedAt:    timestamp(row.CreatedAt).UTC().Format(timeLayout),
			UpdatedAt:    timestamp(row.UpdatedAt).UTC().Format(timeLayout),
		})
	}
	writePaginated(w, items, page, limit, total)
}

type voucherStatsDTO struct {
	TotalVouchers  int64 `json:"totalVouchers"`
	ActiveVouchers int64 `json:"activeVouchers"`
	TotalUsage     int64 `json:"totalUsage"`
}

// VoucherStats handles GET /api/v1/admin/vouchers/stats.
func (h *OrdersHandler) VoucherStats(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	stats, err := h.Q.AdminVoucherStats(r.Context())
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load voucher stats", nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": voucherStatsDTO{
		TotalVouchers:  stats.TotalVouchers,
		ActiveVouchers: stats.ActiveVouchers,
		TotalUsage:     stats.TotalUsage,
	}})
}

// DeleteVoucher handles DELETE /api/v1/admin/vouchers/{code}.
func (h *OrdersHandler) DeleteVoucher(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "admin queries not configured", nil)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(chi.URLParam(r, "code")))
	if code == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "voucher code is required", nil)
		return
	}
	affected, err := h.Q.AdminDeleteVoucher(r.Context(), code)
	if err != nil {
		if isForeignKeyViolation(err) {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "voucher has been used and cannot be deleted", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete voucher", nil)
		return
	}
	if affected == 0 {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "voucher not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
