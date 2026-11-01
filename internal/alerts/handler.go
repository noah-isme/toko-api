package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// API is the service surface the HTTP handler depends on.
type API interface {
	Subscribe(ctx context.Context, userID, tenantID pgtype.UUID, in SubscribeInput) (Subscription, bool, error)
	List(ctx context.Context, userID, tenantID, productID pgtype.UUID, page, limit int32) ([]Subscription, int64, error)
	Unsubscribe(ctx context.Context, id, userID, tenantID pgtype.UUID) (bool, error)
}

// Handler exposes /api/v1/alerts.
type Handler struct {
	Svc API
}

type productDTO struct {
	Title     string  `json:"title"`
	Slug      string  `json:"slug"`
	Thumbnail *string `json:"thumbnail"`
	Price     int64   `json:"price"`
	InStock   bool    `json:"inStock"`
}

type variantDTO struct {
	ID    string  `json:"id"`
	SKU   *string `json:"sku"`
	Price int64   `json:"price"`
	Stock int32   `json:"stock"`
}

type subscriptionDTO struct {
	ID               string      `json:"id"`
	ProductID        string      `json:"productId"`
	VariantID        *string     `json:"variantId"`
	Type             string      `json:"type"`
	PriceAtSubscribe *int64      `json:"priceAtSubscribe"`
	Status           string      `json:"status"`
	NotifiedAt       *string     `json:"notifiedAt"`
	CreatedAt        string      `json:"createdAt"`
	Product          productDTO  `json:"product"`
	Variant          *variantDTO `json:"variant,omitempty"`
}

type subscribeRequest struct {
	ProductID string  `json:"productId"`
	VariantID *string `json:"variantId"`
	Type      string  `json:"type"`
}

// List handles GET /alerts.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	var productID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("productId")); raw != "" {
		id, err := toUUID(raw)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid productId", nil)
			return
		}
		productID = id
	}
	page, perPage := common.ParsePagination(r, 20)
	if page > math.MaxInt32 {
		page = math.MaxInt32
	}
	if perPage > 100 {
		perPage = 100
	}
	items, total, err := h.Svc.List(r.Context(), userID, tenantID, productID, int32(page), int32(perPage))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list alerts", nil)
		return
	}
	data := make([]subscriptionDTO, 0, len(items))
	for i := range items {
		data = append(data, toDTO(items[i]))
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data": data,
		"pagination": common.Pagination{
			Page:       page,
			PerPage:    perPage,
			TotalItems: int(total),
		},
	})
}

// Subscribe handles POST /alerts. 201 on create, 200 on re-arm.
func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	var req subscribeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	productID, err := toUUID(strings.TrimSpace(req.ProductID))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid productId", nil)
		return
	}
	var variantID pgtype.UUID
	if req.VariantID != nil && strings.TrimSpace(*req.VariantID) != "" {
		variantID, err = toUUID(strings.TrimSpace(*req.VariantID))
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid variantId", nil)
			return
		}
	}
	alertType := strings.ToUpper(strings.TrimSpace(req.Type))
	if !ValidType(alertType) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "type must be BACK_IN_STOCK or PRICE_DROP", nil)
		return
	}
	sub, created, err := h.Svc.Subscribe(r.Context(), userID, tenantID, SubscribeInput{
		ProductID: productID,
		VariantID: variantID,
		Type:      alertType,
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrVariantMismatch):
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "product or variant not found", nil)
		return
	case errors.Is(err, ErrAlreadyInStock):
		common.JSONError(w, http.StatusConflict, "ALREADY_IN_STOCK", "product is already in stock", nil)
		return
	case errors.Is(err, ErrInvalidType):
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "type must be BACK_IN_STOCK or PRICE_DROP", nil)
		return
	default:
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to subscribe", nil)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	common.JSON(w, status, map[string]any{"data": toDTO(sub)})
}

// Unsubscribe handles DELETE /alerts/{id}.
func (h *Handler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	id, err := toUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid alert id", nil)
		return
	}
	deleted, err := h.Svc.Unsubscribe(r.Context(), id, userID, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to delete alert", nil)
		return
	}
	if !deleted {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "alert not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toDTO(s Subscription) subscriptionDTO {
	d := subscriptionDTO{
		ID:        uuidString(s.ID),
		ProductID: uuidString(s.ProductID),
		Type:      s.Type,
		Status:    "active",
		CreatedAt: s.CreatedAt.Time.UTC().Format(time.RFC3339),
		Product: productDTO{
			Title:   s.Product.Title,
			Slug:    s.Product.Slug,
			Price:   s.Product.Price,
			InStock: s.Product.InStock,
		},
	}
	if s.VariantID.Valid {
		v := uuidString(s.VariantID)
		d.VariantID = &v
	}
	if s.PriceAtSubscribe.Valid {
		p := s.PriceAtSubscribe.Int64
		d.PriceAtSubscribe = &p
	}
	if s.NotifiedAt.Valid {
		d.Status = "notified"
		n := s.NotifiedAt.Time.UTC().Format(time.RFC3339)
		d.NotifiedAt = &n
	}
	if s.Product.Thumbnail.Valid {
		t := s.Product.Thumbnail.String
		d.Product.Thumbnail = &t
	}
	if s.Variant != nil {
		vd := &variantDTO{ID: uuidString(s.Variant.ID), Price: s.Variant.Price, Stock: s.Variant.Stock}
		if s.Variant.SKU.Valid {
			sku := s.Variant.SKU.String
			vd.SKU = &sku
		}
		d.Variant = vd
	}
	return d
}

// identity resolves the authenticated user and tenant (same contract as
// internal/notifications).
func identity(w http.ResponseWriter, r *http.Request) (userID, tenantID pgtype.UUID, ok bool) {
	ctx := r.Context()
	userIDStr, has := common.UserID(ctx)
	if !has {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "UNAUTHORIZED", nil)
		return userID, tenantID, false
	}
	var err error
	userID, err = toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return userID, tenantID, false
	}
	tenantIDStr, has := tenant.FromContext(ctx)
	if !has {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return userID, tenantID, false
	}
	tenantID, err = toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_TENANT_ID", "invalid tenant id", nil)
		return userID, tenantID, false
	}
	return userID, tenantID, true
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
