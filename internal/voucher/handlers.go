package voucher

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Handler exposes administrative voucher management endpoints.
type Handler struct {
	Q               dbgen.Querier
	Svc             *Service
	Pool            *pgxpool.Pool
	DefaultPriority int
	CatalogCache    *catalog.Cache
	Analytics       *analytics.Service
}

// PublicList returns active vouchers that shoppers can discover without an
// admin session. Eligibility is still evaluated at cart/checkout time; this
// endpoint deliberately exposes rules, not a guaranteed discount.
func (h *Handler) PublicList(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "voucher service not configured", nil)
		return
	}
	tenantID, ok := tenant.FromContext(r.Context())
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	tenantUUID, err := uuid.Parse(strings.TrimSpace(tenantID))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "invalid tenant", nil)
		return
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT id,code,value,kind,percent_bps,min_spend,usage_limit,used_count,
		       per_user_limit,valid_from,valid_to,combinable,priority,
		       product_ids,category_ids,brand_ids,created_at,updated_at
		FROM vouchers
		WHERE tenant_id=$1
		  AND (valid_from IS NULL OR valid_from <= now())
		  AND (valid_to IS NULL OR valid_to > now())
		  AND (usage_limit IS NULL OR used_count < usage_limit)
		ORDER BY priority ASC, valid_to ASC NULLS LAST, created_at DESC`, tenantUUID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list vouchers", nil)
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id pgtype.UUID
		var code, kind string
		var value, minSpend int64
		var percent, usageLimit, perUserLimit pgtype.Int4
		var used int32
		var validFrom, validTo pgtype.Timestamptz
		var combinable bool
		var priority int32
		var productIDs, categoryIDs, brandIDs []pgtype.UUID
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&id, &code, &value, &kind, &percent, &minSpend, &usageLimit, &used, &perUserLimit, &validFrom, &validTo, &combinable, &priority, &productIDs, &categoryIDs, &brandIDs, &createdAt, &updatedAt); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to read vouchers", nil)
			return
		}
		items = append(items, map[string]any{
			"id": uuid.UUID(id.Bytes), "code": code, "kind": kind, "value": value,
			"percentBps": nullableInt4Value(percent), "minSpend": minSpend,
			"usageLimit": nullableInt4Value(usageLimit), "usedCount": used,
			"perUserLimit": nullableInt4Value(perUserLimit), "validFrom": nullableTime(validFrom),
			"validTo": nullableTime(validTo), "combinable": combinable, "priority": priority,
			"productIds": uuidStrings(productIDs), "categoryIds": uuidStrings(categoryIDs),
			"brandIds": uuidStrings(brandIDs), "createdAt": createdAt, "updatedAt": updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to read vouchers", nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

func nullableInt4Value(value pgtype.Int4) any {
	if value.Valid {
		return value.Int32
	}
	return nil
}
func nullableTime(value pgtype.Timestamptz) any {
	if value.Valid {
		return value.Time
	}
	return nil
}
func uuidStrings(values []pgtype.UUID) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value.Valid {
			result = append(result, uuid.UUID(value.Bytes).String())
		}
	}
	return result
}

type voucherPayload struct {
	Code         string     `json:"code"`
	Value        int64      `json:"value"`
	Kind         string     `json:"kind"`
	PercentBps   *int32     `json:"percentBps"`
	MinSpend     int64      `json:"minSpend"`
	UsageLimit   *int32     `json:"usageLimit"`
	ValidFrom    *time.Time `json:"validFrom"`
	ValidTo      *time.Time `json:"validTo"`
	ProductIDs   []string   `json:"productIds"`
	CategoryIDs  []string   `json:"categoryIds"`
	BrandIDs     []string   `json:"brandIds"`
	Combinable   *bool      `json:"combinable"`
	Priority     *int       `json:"priority"`
	PerUserLimit *int32     `json:"perUserLimit"`
}

type previewRequest struct {
	Code      string               `json:"code"`
	CartTotal int64                `json:"cartTotal"`
	UserID    *string              `json:"userId"`
	Items     []previewRequestItem `json:"items"`
}

type previewRequestItem struct {
	ProductID  *string `json:"productId"`
	CategoryID *string `json:"categoryId"`
	BrandID    *string `json:"brandId"`
	Subtotal   int64   `json:"subtotal"`
}

// Create inserts a new voucher rule.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "voucher queries not configured", nil)
		return
	}
	var payload voucherPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	params, err := buildCreateParams(payload, h.DefaultPriority)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	ctx := r.Context()
	voucher, err := h.Q.CreateVoucher(ctx, params)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			common.JSONError(w, http.StatusConflict, "CONFLICT", "voucher code already exists", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create voucher", nil)
		return
	}
	h.invalidateCaches(ctx)
	common.JSON(w, http.StatusCreated, map[string]any{"data": voucher})
}

// Update mutates an existing voucher identified by code.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "voucher queries not configured", nil)
		return
	}
	code := strings.TrimSpace(chi.URLParam(r, "code"))
	if code == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "code is required", nil)
		return
	}
	var payload voucherPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	params, err := buildUpdateParams(code, payload, h.DefaultPriority)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	ctx := r.Context()
	voucher, err := h.Q.UpdateVoucher(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "voucher not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to update voucher", nil)
		return
	}
	h.invalidateCaches(ctx)
	common.JSON(w, http.StatusOK, map[string]any{"data": voucher})
}

// Preview returns the simulated discount for a voucher without persisting state.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "voucher service not configured", nil)
		return
	}
	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	items, err := toEngineItems(req.Items)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	result, err := h.Svc.Preview(r.Context(), req.Code, req.UserID, req.CartTotal, items)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "NOT_ELIGIBLE", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": result})
}

func buildCreateParams(payload voucherPayload, defaultPriority int) (dbgen.CreateVoucherParams, error) {
	code := strings.TrimSpace(payload.Code)
	if code == "" {
		return dbgen.CreateVoucherParams{}, errors.New("code is required")
	}
	kind := strings.TrimSpace(payload.Kind)
	if kind == "" {
		kind = "fixed_amount"
	}
	dk := dbgen.DiscountKind(kind)
	switch dk {
	case dbgen.DiscountKindFixedAmount, dbgen.DiscountKindPercent:
	default:
		return dbgen.CreateVoucherParams{}, errors.New("invalid kind")
	}
	percent := pgtype.Int4{}
	if payload.PercentBps != nil {
		percent = pgtype.Int4{Int32: *payload.PercentBps, Valid: true}
	}
	usageLimit := pgtype.Int4{}
	if payload.UsageLimit != nil {
		usageLimit = pgtype.Int4{Int32: *payload.UsageLimit, Valid: true}
	}
	perUser := pgtype.Int4{}
	if payload.PerUserLimit != nil {
		perUser = pgtype.Int4{Int32: *payload.PerUserLimit, Valid: true}
	}
	validFrom := timeToNullable(payload.ValidFrom)
	validTo := timeToNullable(payload.ValidTo)
	productIDs, err := toUUIDArray(payload.ProductIDs)
	if err != nil {
		return dbgen.CreateVoucherParams{}, err
	}
	categoryIDs, err := toUUIDArray(payload.CategoryIDs)
	if err != nil {
		return dbgen.CreateVoucherParams{}, err
	}
	brandIDs, err := toUUIDArray(payload.BrandIDs)
	if err != nil {
		return dbgen.CreateVoucherParams{}, err
	}
	priority := int32(defaultPriority)
	if payload.Priority != nil {
		priority = int32(*payload.Priority)
	}
	combinable := false
	if payload.Combinable != nil {
		combinable = *payload.Combinable
	}
	return dbgen.CreateVoucherParams{
		Code:         code,
		Value:        payload.Value,
		Kind:         dk,
		PercentBps:   percent,
		MinSpend:     payload.MinSpend,
		UsageLimit:   usageLimit,
		ValidFrom:    validFrom,
		ValidTo:      validTo,
		ProductIds:   productIDs,
		CategoryIds:  categoryIDs,
		BrandIds:     brandIDs,
		Combinable:   combinable,
		Priority:     priority,
		PerUserLimit: perUser,
	}, nil
}

func buildUpdateParams(code string, payload voucherPayload, defaultPriority int) (dbgen.UpdateVoucherParams, error) {
	params, err := buildCreateParams(payload, defaultPriority)
	if err != nil {
		return dbgen.UpdateVoucherParams{}, err
	}
	return dbgen.UpdateVoucherParams{
		Code:         code,
		Value:        params.Value,
		Kind:         params.Kind,
		PercentBps:   params.PercentBps,
		MinSpend:     params.MinSpend,
		UsageLimit:   params.UsageLimit,
		ValidFrom:    params.ValidFrom,
		ValidTo:      params.ValidTo,
		ProductIds:   params.ProductIds,
		CategoryIds:  params.CategoryIds,
		BrandIds:     params.BrandIds,
		Combinable:   params.Combinable,
		Priority:     params.Priority,
		PerUserLimit: params.PerUserLimit,
	}, nil
}

func toUUIDArray(values []string) ([]pgtype.UUID, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]pgtype.UUID, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		parsed, err := parseUUID(trimmed)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

func timeToNullable(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *v, Valid: true}
}

func toEngineItems(items []previewRequestItem) ([]Item, error) {
	if len(items) == 0 {
		return nil, errors.New("items are required for preview")
	}
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if it.Subtotal <= 0 {
			continue
		}
		item := Item{Subtotal: it.Subtotal}
		if it.ProductID != nil && strings.TrimSpace(*it.ProductID) != "" {
			parsed, err := uuidFromString(*it.ProductID)
			if err != nil {
				return nil, err
			}
			item.ProductID = &parsed
		}
		if it.CategoryID != nil && strings.TrimSpace(*it.CategoryID) != "" {
			parsed, err := uuidFromString(*it.CategoryID)
			if err != nil {
				return nil, err
			}
			item.CategoryID = &parsed
		}
		if it.BrandID != nil && strings.TrimSpace(*it.BrandID) != "" {
			parsed, err := uuidFromString(*it.BrandID)
			if err != nil {
				return nil, err
			}
			item.BrandID = &parsed
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, errors.New("no valid items provided")
	}
	return out, nil
}

func uuidFromString(value string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.UUID{}, err
	}
	return parsed, nil
}

func (h *Handler) invalidateCaches(ctx context.Context) {
	if h == nil {
		return
	}
	if h.CatalogCache != nil {
		h.CatalogCache.InvalidateList(ctx)
	}
	if h.Analytics != nil {
		h.Analytics.Clear(ctx)
	}
}
