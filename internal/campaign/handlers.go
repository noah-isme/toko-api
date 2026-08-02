package campaign

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Handler exposes public and admin flash-sale campaign endpoints.
type Handler struct{ Pool *pgxpool.Pool }

type campaignInput struct {
	Name     string      `json:"name"`
	Slug     string      `json:"slug"`
	Status   string      `json:"status"`
	StartsAt time.Time   `json:"startsAt"`
	EndsAt   time.Time   `json:"endsAt"`
	Items    []itemInput `json:"items"`
}

type itemInput struct {
	ProductID  string `json:"productId"`
	SalePrice  int64  `json:"salePrice"`
	StockLimit *int32 `json:"stockLimit"`
}

type publicItem struct {
	ID            string  `json:"id"`
	ProductID     string  `json:"productId"`
	Title         string  `json:"title"`
	Slug          string  `json:"slug"`
	OriginalPrice int64   `json:"originalPrice"`
	SalePrice     int64   `json:"salePrice"`
	DiscountBps   int32   `json:"discountBps"`
	Stock         int     `json:"stock"`
	StockLimit    *int32  `json:"stockLimit,omitempty"`
	SoldCount     int32   `json:"soldCount"`
	Thumbnail     *string `json:"thumbnail,omitempty"`
}

func (h *Handler) Public(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "campaign service not configured", nil)
		return
	}
	tenantID, ok := tenantUUID(r)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT c.id,c.name,c.slug,c.status,c.starts_at,c.ends_at,
		       i.id,i.product_id,p.title,p.slug,p.price,i.sale_price,
		       i.stock_limit,i.sold_count,p.thumbnail,
		       CASE WHEN p.price > 0 THEN ((p.price-i.sale_price)*10000/p.price)::int ELSE 0 END
		FROM flash_sale_campaigns c
		JOIN flash_sale_items i ON i.campaign_id=c.id
		JOIN products p ON p.id=i.product_id AND p.tenant_id=c.tenant_id
		WHERE c.tenant_id=$1 AND c.status IN ('SCHEDULED','ACTIVE')
		  AND c.starts_at <= now() + interval '90 days' AND c.ends_at > now()
		ORDER BY c.starts_at ASC, i.created_at ASC`, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to list flash sales", nil)
		return
	}
	defer rows.Close()
	campaigns := map[string]*map[string]any{}
	ordered := make([]*map[string]any, 0)
	for rows.Next() {
		var campaignID, itemID, productID pgtype.UUID
		var name, slug, status, title, productSlug string
		var startsAt, endsAt time.Time
		var original, sale int64
		var stockLimit pgtype.Int4
		var sold int32
		var thumbnail pgtype.Text
		var discount int32
		if err := rows.Scan(&campaignID, &name, &slug, &status, &startsAt, &endsAt, &itemID, &productID, &title, &productSlug, &original, &sale, &stockLimit, &sold, &thumbnail, &discount); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to read flash sales", nil)
			return
		}
		key := cart.UUIDString(campaignID)
		current, exists := campaigns[key]
		if !exists {
			body := map[string]any{"id": key, "name": name, "slug": slug, "status": status, "startsAt": startsAt, "endsAt": endsAt, "items": []publicItem{}}
			campaigns[key] = &body
			ordered = append(ordered, &body)
			current = &body
		}
		items := (*current)["items"].([]publicItem)
		stock := 0
		if stockLimit.Valid {
			stock = int(stockLimit.Int32 - sold)
		} else {
			// The catalog stock is the fallback when the campaign has no quota.
			_ = h.Pool.QueryRow(r.Context(), `SELECT COALESCE(SUM(stock),0)::int FROM product_variants WHERE product_id=$1`, productID).Scan(&stock)
		}
		if stock < 0 {
			stock = 0
		}
		items = append(items, publicItem{ID: cart.UUIDString(itemID), ProductID: cart.UUIDString(productID), Title: title, Slug: productSlug, OriginalPrice: original, SalePrice: sale, DiscountBps: discount, Stock: stock, StockLimit: nullableInt4(stockLimit), SoldCount: sold, Thumbnail: nullableText(thumbnail)})
		(*current)["items"] = items
	}
	if err := rows.Err(); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to read flash sales", nil)
		return
	}
	result := make([]map[string]any, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, *item)
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		common.JSONError(w, 500, "INTERNAL", "campaign service not configured", nil)
		return
	}
	tenantID, ok := tenantUUID(r)
	if !ok {
		common.JSONError(w, 400, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	var input campaignInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if err := validateInput(input); err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", err.Error(), nil)
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to begin campaign", nil)
		return
	}
	defer tx.Rollback(r.Context())
	var campaignID pgtype.UUID
	err = tx.QueryRow(r.Context(), `INSERT INTO flash_sale_campaigns(tenant_id,name,slug,status,starts_at,ends_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, tenantID, strings.TrimSpace(input.Name), strings.TrimSpace(input.Slug), normalizedStatus(input.Status), input.StartsAt, input.EndsAt).Scan(&campaignID)
	if err != nil {
		common.JSONError(w, http.StatusConflict, "CONFLICT", "campaign slug already exists or is invalid", nil)
		return
	}
	if err := insertItems(r, tx, campaignID, input.Items); err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to save campaign", nil)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": cart.UUIDString(campaignID)}})
}

func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		common.JSONError(w, 500, "INTERNAL", "campaign service not configured", nil)
		return
	}
	tenantID, ok := tenantUUID(r)
	if !ok {
		common.JSONError(w, 400, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	campaignID, err := cart.ToUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid campaign id", nil)
		return
	}
	var input statusPayload
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	status := normalizedStatus(input.Status)
	if status == "DRAFT" || status == "SCHEDULED" || status == "ACTIVE" || status == "ENDED" {
		var updated time.Time
		err = h.Pool.QueryRow(r.Context(), `UPDATE flash_sale_campaigns SET status=$1,updated_at=now() WHERE id=$2 AND tenant_id=$3 RETURNING updated_at`, status, campaignID, tenantID).Scan(&updated)
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, 404, "NOT_FOUND", "campaign not found", nil)
			return
		}
		if err != nil {
			common.JSONError(w, 500, "INTERNAL", "failed to update campaign", nil)
			return
		}
		common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(campaignID), "status": status, "updatedAt": updated}})
		return
	}
	common.JSONError(w, 400, "BAD_REQUEST", "unsupported campaign status", nil)
}

type statusPayload struct {
	Status string `json:"status"`
}

func validateInput(input campaignInput) error {
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Slug) == "" {
		return errors.New("name and slug are required")
	}
	if input.StartsAt.IsZero() || input.EndsAt.IsZero() || !input.EndsAt.After(input.StartsAt) {
		return errors.New("startsAt and endsAt must define a valid range")
	}
	if len(input.Items) == 0 {
		return errors.New("at least one item is required")
	}
	for _, item := range input.Items {
		if _, err := cart.ToUUID(item.ProductID); err != nil {
			return errors.New("item productId is invalid")
		}
		if item.SalePrice < 0 {
			return errors.New("salePrice cannot be negative")
		}
	}
	return nil
}

func insertItems(r *http.Request, tx pgx.Tx, campaignID pgtype.UUID, items []itemInput) error {
	for _, item := range items {
		productID, _ := cart.ToUUID(item.ProductID)
		if _, err := tx.Exec(r.Context(), `INSERT INTO flash_sale_items(campaign_id,product_id,sale_price,stock_limit) VALUES($1,$2,$3,$4)`, campaignID, productID, item.SalePrice, item.StockLimit); err != nil {
			return err
		}
	}
	return nil
}

func tenantUUID(r *http.Request) (pgtype.UUID, bool) {
	value, ok := tenant.FromContext(r.Context())
	if !ok {
		return pgtype.UUID{}, false
	}
	id, err := cart.ToUUID(value)
	return id, err == nil
}
func normalizedStatus(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "DRAFT"
	}
	return value
}
func nullableInt4(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	v := value.Int32
	return &v
}
func nullableText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}
