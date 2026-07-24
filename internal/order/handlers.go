package order

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type Handler struct {
	Q *dbgen.Queries
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "order queries not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || userID == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	page, perPage := common.ParsePagination(r, 20)
	if perPage > 100 {
		perPage = 100
	}
	offset := int32((page - 1) * perPage)
	uID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}
	total, err := h.Q.CountOrdersForUser(r.Context(), uID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to count orders", nil)
		return
	}
	orders, err := h.Q.ListOrdersForUser(r.Context(), dbgen.ListOrdersForUserParams{UserID: uID, Limit: int32(perPage), Offset: offset})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list orders", nil)
		return
	}
	response := make([]map[string]any, 0, len(orders))
	for _, ord := range orders {
		status := strings.ToLower(string(ord.Status))

		itemCount, _ := h.Q.CountOrderItems(r.Context(), ord.ID)

		thumbnailUrl := ""
		if thumb, tErr := h.Q.GetOrderThumbnail(r.Context(), ord.ID); tErr == nil {
			thumbnailUrl = thumb
		}

		paymentMethod := ""
		if pay, pErr := h.Q.GetLatestPaymentByOrder(r.Context(), ord.ID); pErr == nil && pay.Channel.Valid {
			paymentMethod = pay.Channel.String
		}

		response = append(response, map[string]any{
			"id":            cart.UUIDString(ord.ID),
			"orderNumber":   nullableText(ord.OrderNumber),
			"status":        status,
			"statusLabel":   statusLabel(status),
			"total":         ord.PricingTotal,
			"currency":      ord.Currency,
			"itemCount":     itemCount,
			"thumbnailUrl":  thumbnailUrl,
			"paymentMethod": paymentMethod,
			"createdAt":     ord.CreatedAt,
			"updatedAt":     ord.UpdatedAt,
		})
	}
	w.Header().Set("X-Total-Count", strconv.FormatInt(total, 10))
	common.JSON(w, http.StatusOK, map[string]any{
		"data": response,
		"pagination": common.Pagination{
			Page:       page,
			PerPage:    perPage,
			TotalItems: int(total),
		},
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "order queries not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || userID == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	orderID := chi.URLParam(r, "orderId")
	oID, err := cart.ToUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	uID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}
	ord, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: oID, UserID: uID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order", nil)
		return
	}

	status := strings.ToLower(string(ord.Status))

	items, err := h.Q.ListOrderItemsByOrder(r.Context(), ord.ID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order items", nil)
		return
	}

	itemCount := int64(len(items))
	thumbnailUrl := ""
	responseItems := make([]map[string]any, 0, len(items))
	for _, it := range items {
		itemImages, _ := h.Q.ListImagesByProduct(r.Context(), it.ProductID)
		imageUrl := ""
		if len(itemImages) > 0 {
			imageUrl = itemImages[0].Url
		}
		if thumbnailUrl == "" && imageUrl != "" {
			thumbnailUrl = imageUrl
		}
		item := map[string]any{
			"id":           cart.UUIDString(it.ID),
			"productId":    cart.UUIDString(it.ProductID),
			"productTitle": it.Title,
			"productSlug":  it.Slug,
			"qty":          it.Qty,
			"unitPrice":    it.UnitPrice,
			"subtotal":     it.Subtotal,
			"imageUrl":     imageUrl,
		}
		if it.VariantID.Valid {
			item["variantId"] = cart.UUIDString(it.VariantID)
		}
		responseItems = append(responseItems, item)
	}

	paymentMethod := ""
	paymentObj := map[string]any{
		"method":      "",
		"methodLabel": "",
		"status":      "pending",
	}
	if pay, pErr := h.Q.GetLatestPaymentByOrder(r.Context(), ord.ID); pErr == nil {
		if pay.Channel.Valid {
			paymentMethod = pay.Channel.String
		}
		paymentObj["method"] = paymentMethod
		paymentObj["methodLabel"] = paymentMethodLabel(paymentMethod)
		paymentObj["status"] = paymentStatusLabel(string(pay.Status))
		if pay.RedirectUrl.Valid && pay.RedirectUrl.String != "" {
			paymentObj["paymentUrl"] = pay.RedirectUrl.String
		}
		if pay.ExpiresAt.Valid {
			paymentObj["expiryAt"] = pay.ExpiresAt
		}
	}

	shippingObj := (*map[string]any)(nil)
	if ship, sErr := h.Q.GetShipmentByOrder(r.Context(), ord.ID); sErr == nil {
		s := map[string]any{
			"courier":   textOrEmpty(ship.Courier),
			"service":   "",
			"shippedAt": ship.LastEventAt,
		}
		if ship.TrackingNumber.Valid {
			s["trackingNumber"] = ship.TrackingNumber.String
		}
		shippingObj = &s
	}

	voucherObj := (*map[string]any)(nil)
	if ord.AppliedVoucherCode.Valid && ord.AppliedVoucherCode.String != "" {
		voucherObj = &map[string]any{
			"code":     ord.AppliedVoucherCode.String,
			"discount": ord.PricingDiscount,
		}
	}

	var userObj any = nil
	if ord.UserID.Valid {
		if user, uErr := h.Q.GetUserByID(r.Context(), ord.UserID); uErr == nil {
			userObj = map[string]any{
				"id":    cart.UUIDString(user.ID),
				"name":  user.Name,
				"email": user.Email,
			}
		}
	}

	statusHistory := buildStatusHistory(ord, status)

	result := map[string]any{
		"id":              cart.UUIDString(ord.ID),
		"orderNumber":     nullableText(ord.OrderNumber),
		"status":          status,
		"statusLabel":     statusLabel(status),
		"total":           ord.PricingTotal,
		"currency":        ord.Currency,
		"itemCount":       itemCount,
		"thumbnailUrl":    thumbnailUrl,
		"paymentMethod":   paymentMethod,
		"createdAt":       ord.CreatedAt,
		"updatedAt":       ord.UpdatedAt,
		"user":            userObj,
		"items":           responseItems,
		"shippingAddress": jsonValue(ord.ShippingAddress, len(ord.ShippingAddress) > 0),
		"pricing": map[string]any{
			"subtotal": ord.PricingSubtotal,
			"discount": ord.PricingDiscount,
			"tax":      ord.PricingTax,
			"shipping": ord.PricingShipping,
			"total":    ord.PricingTotal,
		},
		"payment":       paymentObj,
		"notes":         nullableText(ord.Notes),
		"statusHistory": statusHistory,
	}
	if voucherObj != nil {
		result["voucher"] = *voucherObj
	}
	if shippingObj != nil {
		result["shipping"] = *shippingObj
	}

	common.JSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "order queries not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || userID == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	orderID := chi.URLParam(r, "orderId")
	oID, err := cart.ToUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	uID, err := cart.ToUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}
	ord, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: oID, UserID: uID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "order lookup failed", nil)
		return
	}
	if ord.Status != "PENDING_PAYMENT" {
		common.JSONError(w, http.StatusBadRequest, "INVALID_STATE", "only pending orders can be canceled", nil)
		return
	}
	if err := h.Q.UpdateOrderStatus(r.Context(), dbgen.UpdateOrderStatusParams{ID: ord.ID, Status: "CANCELED"}); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to cancel order", nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"status": "cancelled"}})
}

func buildStatusHistory(ord dbgen.Order, currentStatus string) []map[string]any {
	history := []map[string]any{
		{"status": "pending_payment", "timestamp": ord.CreatedAt},
	}
	if currentStatus == "cancelled" || currentStatus == "canceled" {
		history = append(history, map[string]any{"status": "cancelled", "timestamp": ord.UpdatedAt})
		return history
	}
	if currentStatus != "pending_payment" {
		history = append(history, map[string]any{"status": currentStatus, "timestamp": ord.UpdatedAt})
	}
	return history
}

func nullableText(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

func nullableUUID(v pgtype.UUID) *string {
	if !v.Valid {
		return nil
	}
	s := cart.UUIDString(v)
	return &s
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func jsonValue(b []byte, valid bool) any {
	if !valid || len(b) == 0 {
		return nil
	}
	clone := make([]byte, len(b))
	copy(clone, b)
	return json.RawMessage(clone)
}
