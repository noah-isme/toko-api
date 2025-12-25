package cart

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/pricing"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

// Handler wires cart services to HTTP.
type Handler struct {
	Q              *dbgen.Queries
	Svc            *Service
	ShippingClient shipping.Client
	ShippingOrigin string
	TaxBps         int
	Currency       string
}

// Create creates or returns a guest cart identifier.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	var payload struct {
		AnonID string `json:"anonId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&payload)
	anonID := strings.TrimSpace(payload.AnonID)
	if anonID == "" {
		anonID = uuid.NewString()
	}
	cart, err := h.Svc.EnsureCart(r.Context(), nil, &anonID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"cartId":  UUIDString(cart.ID),
			"anonId":  anonID,
			"voucher": nullableText(cart.AppliedVoucherCode),
		},
	})
}

// Get returns cart contents and pricing preview.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart queries not configured", nil)
		return
	}
	idParam := chi.URLParam(r, "id")
	cID, err := toUUID(idParam)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid cart id", nil)
		return
	}
	cart, err := h.Q.GetCartByID(r.Context(), cID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "cart not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "unable to load cart", nil)
		return
	}
	now := time.Now()
	if h.Svc != nil {
		now = h.Svc.now()
	}
	if cart.ExpiresAt.Valid && cart.ExpiresAt.Time.Before(now) {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "cart expired", nil)
		return
	}
	items, err := h.Q.ListCartItems(r.Context(), cID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "unable to load cart items", nil)
		return
	}
	responseItems := make([]map[string]any, 0, len(items))
	pricingItems := make([]pricing.Item, 0, len(items))
	for _, it := range items {
		responseItems = append(responseItems, map[string]any{
			"id":        UUIDString(it.ID),
			"productId": UUIDString(it.ProductID),
			"variantId": nullableUUID(it.VariantID),
			"title":     it.Title,
			"slug":      it.Slug,
			"qty":       it.Qty,
			"unitPrice": it.UnitPrice,
			"subtotal":  it.Subtotal,
		})
		pricingItems = append(pricingItems, pricing.Item{Qty: int(it.Qty), UnitPrice: pricing.Money(it.UnitPrice)})
	}
	var discount int64
	if cart.AppliedVoucherCode.Valid && cart.AppliedVoucherCode.String != "" && h.Svc != nil {
		cartModel := dbgen.Cart{
			ID:                 cart.ID,
			UserID:             cart.UserID,
			AnonID:             cart.AnonID,
			AppliedVoucherCode: cart.AppliedVoucherCode,
			CreatedAt:          cart.CreatedAt,
			UpdatedAt:          cart.UpdatedAt,
			ExpiresAt:          cart.ExpiresAt,
		}
		discount, _, err = h.Svc.evaluateVoucher(r.Context(), cartModel, cart.AppliedVoucherCode.String)
		if err != nil {
			discount = 0
		}
	}
	summary := pricing.Compute(pricingItems, discount, h.TaxBps, 0)
	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id":      UUIDString(cart.ID),
			"anonId":  nullableText(cart.AnonID),
			"voucher": nullableText(cart.AppliedVoucherCode),
			"items":   responseItems,
			"pricing": map[string]any{
				"subtotal": summary.Subtotal,
				"discount": summary.Discount,
				"tax":      summary.Tax,
				"shipping": summary.Shipping,
				"total":    summary.Total,
			},
			"currency": h.Currency,
		},
	})
}

// GetActive resolves the current active cart for the user or anon ID.
func (h *Handler) GetActive(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	ctx := r.Context()
	
	// Try to resolve user ID first
	var userID *string
	if uID, ok := common.UserID(ctx); ok && strings.TrimSpace(uID) != "" {
		userID = &uID
	}

	// Try to resolve anon ID from query param
	var anonID *string
	if aID := r.URL.Query().Get("anonId"); strings.TrimSpace(aID) != "" {
		anonID = &aID
	}

	if userID == nil && anonID == nil {
		common.JSONError(w, http.StatusOK, "NO_CONTENT", "no active cart context", nil)
		return
	}

	cart, err := h.Svc.EnsureCart(ctx, userID, anonID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id":      UUIDString(cart.ID),
			"anonId":  nullableText(cart.AnonID),
			"voucher": nullableText(cart.AppliedVoucherCode),
		},
	})
}

// AddItem adds or increments a cart line item.
func (h *Handler) AddItem(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	cartID := chi.URLParam(r, "id")
	var payload struct {
		ProductID string  `json:"productId"`
		VariantID *string `json:"variantId"`
		Qty       int     `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if payload.ProductID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "productId is required", nil)
		return
	}
	if payload.Qty <= 0 {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "qty must be positive", nil)
		return
	}
	if err := h.Svc.AddItem(r.Context(), cartID, payload.ProductID, payload.VariantID, payload.Qty); err != nil {
		h.writeError(w, err)
		return
	}
	h.Get(w, r)
}

// UpdateItem updates the quantity for a cart line item.
func (h *Handler) UpdateItem(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	itemID := chi.URLParam(r, "itemId")
	var payload struct {
		Qty int `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if payload.Qty <= 0 {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "qty must be positive", nil)
		return
	}
	if err := h.Svc.UpdateQty(r.Context(), itemID, payload.Qty); err != nil {
		h.writeError(w, err)
		return
	}
	h.Get(w, r)
}

// RemoveItem deletes a cart item.
func (h *Handler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	cartID := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "itemId")
	if err := h.Svc.RemoveItem(r.Context(), cartID, itemID); err != nil {
		h.writeError(w, err)
		return
	}
	h.Get(w, r)
}

// ApplyVoucher applies a voucher to the cart.
func (h *Handler) ApplyVoucher(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	cartID := chi.URLParam(r, "id")
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	payload.Code = strings.TrimSpace(payload.Code)
	discount, err := h.Svc.ApplyVoucher(r.Context(), cartID, payload.Code)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"discount": discount}})
}

// RemoveVoucher removes the applied voucher from the cart.
func (h *Handler) RemoveVoucher(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	cartID := chi.URLParam(r, "id")
	if err := h.Svc.RemoveVoucher(r.Context(), cartID); err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"voucher": nil}})
}

// QuoteShipping returns shipping rates from the configured provider.
func (h *Handler) QuoteShipping(w http.ResponseWriter, r *http.Request) {
	if h.ShippingClient == nil {
		common.JSONError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "shipping provider not configured", nil)
		return
	}
	cartID := chi.URLParam(r, "id")
	var payload struct {
		Destination string `json:"destination"`
		Courier     string `json:"courier"`
		WeightGram  int    `json:"weightGram"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if payload.Destination == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "destination is required", nil)
		return
	}
	if payload.WeightGram <= 0 {
		payload.WeightGram = 1000
	}
	cID, err := toUUID(cartID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid cart id", nil)
		return
	}
	if h.Q != nil {
		if _, err := h.Q.GetCartByID(r.Context(), cID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "cart not found", nil)
				return
			}
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "unable to load cart", nil)
			return
		}
	}
	rates, err := h.ShippingClient.Rates(r.Context(), shipping.RateReq{
		Origin:      h.ShippingOrigin,
		Destination: payload.Destination,
		WeightGram:  payload.WeightGram,
		Courier:     payload.Courier,
	})
	if err != nil {
		common.JSONError(w, http.StatusBadGateway, "SHIPPING_ERROR", "failed to fetch rates", nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rates})
}

// QuoteTax returns the expected tax amount for the current cart.
func (h *Handler) QuoteTax(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart queries not configured", nil)
		return
	}
	idParam := chi.URLParam(r, "id")
	cID, err := toUUID(idParam)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid cart id", nil)
		return
	}
	items, err := h.Q.ListCartItems(r.Context(), cID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "unable to load cart items", nil)
		return
	}
	pricingItems := make([]pricing.Item, 0, len(items))
	for _, it := range items {
		pricingItems = append(pricingItems, pricing.Item{Qty: int(it.Qty), UnitPrice: pricing.Money(it.UnitPrice)})
	}
	summary := pricing.Compute(pricingItems, 0, h.TaxBps, 0)
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"tax": summary.Tax}})
}

// Merge merges a guest cart into the authenticated user's cart.
func (h *Handler) Merge(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "cart service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || userID == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	var payload struct {
		CartID string `json:"cartId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	payload.CartID = strings.TrimSpace(payload.CartID)
	if payload.CartID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "cartId is required", nil)
		return
	}
	mergedID, err := h.Svc.Merge(r.Context(), payload.CartID, userID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"cartId": mergedID}})
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	if err == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "unknown error", nil)
		return
	}
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		status := appErr.HTTPStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		code := appErr.Code
		if code == "" {
			code = "BAD_REQUEST"
		}
		common.JSONError(w, status, code, appErr.Message, appErr.Details)
		return
	}
	switch {
	case errors.Is(err, ErrInvalidInput):
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
	case errors.Is(err, ErrNotFound):
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	default:
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
}

func nullableText(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullableUUID(v pgtype.UUID) *string {
	if !v.Valid {
		return nil
	}
	s := uuid.UUID(v.Bytes).String()
	return &s
}
