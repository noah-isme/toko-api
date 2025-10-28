package order

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
		response = append(response, map[string]any{
			"id":        cart.UUIDString(ord.ID),
			"status":    ord.Status,
			"total":     ord.PricingTotal,
			"subtotal":  ord.PricingSubtotal,
			"discount":  ord.PricingDiscount,
			"tax":       ord.PricingTax,
			"shipping":  ord.PricingShipping,
			"currency":  ord.Currency,
			"createdAt": ord.CreatedAt,
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
	items, err := h.Q.ListOrderItemsByOrder(r.Context(), ord.ID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order items", nil)
		return
	}
	responseItems := make([]map[string]any, 0, len(items))
	for _, it := range items {
		responseItems = append(responseItems, map[string]any{
			"id":        cart.UUIDString(it.ID),
			"productId": cart.UUIDString(it.ProductID),
			"variantId": nullableUUID(it.VariantID),
			"title":     it.Title,
			"slug":      it.Slug,
			"qty":       it.Qty,
			"unitPrice": it.UnitPrice,
			"subtotal":  it.Subtotal,
		})
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id":              cart.UUIDString(ord.ID),
			"status":          ord.Status,
			"total":           ord.PricingTotal,
			"subtotal":        ord.PricingSubtotal,
			"discount":        ord.PricingDiscount,
			"tax":             ord.PricingTax,
			"shipping":        ord.PricingShipping,
			"currency":        ord.Currency,
			"createdAt":       ord.CreatedAt,
			"items":           responseItems,
			"notes":           nullableText(ord.Notes),
			"shippingAddress": jsonValue(ord.ShippingAddress, len(ord.ShippingAddress) > 0),
			"shippingOption":  jsonValue(ord.ShippingOption, len(ord.ShippingOption) > 0),
		},
	})
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
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"status": "CANCELED"}})
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

func jsonValue(b []byte, valid bool) any {
	if !valid || len(b) == 0 {
		return nil
	}
	clone := make([]byte, len(b))
	copy(clone, b)
	return json.RawMessage(clone)
}
