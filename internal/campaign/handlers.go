package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

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
		notConfigured(r.Context(), w)
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
		internalError(r.Context(), w, err, "failed to list flash sales")
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
			internalError(r.Context(), w, err, "failed to read flash sales")
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
		internalError(r.Context(), w, err, "failed to read flash sales")
		return
	}
	result := make([]map[string]any, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, *item)
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *Handler) AdminGet(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		notConfigured(r.Context(), w)
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
	campaign, err := h.loadCampaign(r.Context(), campaignID, tenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		common.JSONError(w, 404, "NOT_FOUND", "campaign not found", nil)
		return
	}
	if err != nil {
		internalError(r.Context(), w, err, "failed to read flash sale")
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": campaign})
}

// loadCampaign reads one tenant-scoped campaign with its items in the admin
// response shape. A campaign without visible items has `items: []`. It returns
// pgx.ErrNoRows only when no campaign matched.
func (h *Handler) loadCampaign(ctx context.Context, campaignID, tenantID pgtype.UUID) (map[string]any, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT c.id,c.name,c.slug,c.status,c.starts_at,c.ends_at,c.created_at,c.updated_at,
		       i.id,i.product_id,p.id,p.title,p.slug,p.price,i.sale_price,
		       i.stock_limit,i.sold_count,p.thumbnail,
		       CASE WHEN p.price > 0 THEN ((p.price-i.sale_price)*10000/p.price)::int ELSE 0 END
		FROM flash_sale_campaigns c
		LEFT JOIN flash_sale_items i ON i.campaign_id=c.id
		LEFT JOIN products p ON p.id=i.product_id AND p.tenant_id=c.tenant_id
		WHERE c.id=$1 AND c.tenant_id=$2
		ORDER BY i.created_at ASC`, campaignID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying campaign: %w", err)
	}
	defer rows.Close()

	var campaign map[string]any
	items := make([]publicItem, 0)
	// Items without a stock limit fall back to catalog stock; looked up after
	// the rows are drained so the query does not hold a second connection.
	var fallback []int
	var fallbackProducts []pgtype.UUID
	for rows.Next() {
		var cID pgtype.UUID
		var name, slug, status string
		var startsAt, endsAt, createdAt, updatedAt time.Time
		var row itemRow
		if err := rows.Scan(append([]any{&cID, &name, &slug, &status, &startsAt, &endsAt, &createdAt, &updatedAt}, row.targets()...)...); err != nil {
			return nil, fmt.Errorf("scanning campaign: %w", err)
		}
		if campaign == nil {
			campaign = map[string]any{
				"id":        cart.UUIDString(cID),
				"name":      name,
				"slug":      slug,
				"status":    status,
				"startsAt":  startsAt,
				"endsAt":    endsAt,
				"createdAt": createdAt,
				"updatedAt": updatedAt,
				"items":     []publicItem{},
			}
		}
		if !row.visible() {
			continue
		}
		item := row.item()
		if !row.StockLimit.Valid {
			fallback = append(fallback, len(items))
			fallbackProducts = append(fallbackProducts, row.ProductID)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading campaign: %w", err)
	}
	if campaign == nil {
		return nil, pgx.ErrNoRows
	}
	for i, idx := range fallback {
		items[idx].Stock = h.catalogStock(ctx, fallbackProducts[i], tenantID)
	}
	campaign["items"] = items
	return campaign, nil
}

// itemRow holds the nullable item/product columns of a campaign LEFT JOIN.
type itemRow struct {
	ItemID, ProductID, JoinedProductID pgtype.UUID
	Title, ProductSlug                 pgtype.Text
	Original, Sale                     pgtype.Int8
	StockLimit                         pgtype.Int4
	Sold                               pgtype.Int4
	Thumbnail                          pgtype.Text
	Discount                           pgtype.Int4
}

func (r *itemRow) targets() []any {
	return []any{&r.ItemID, &r.ProductID, &r.JoinedProductID, &r.Title, &r.ProductSlug, &r.Original, &r.Sale, &r.StockLimit, &r.Sold, &r.Thumbnail, &r.Discount}
}

// visible reports whether the row carries an item whose product belongs to the
// campaign's tenant. Items pointing at another tenant's product stay hidden.
func (r *itemRow) visible() bool { return r.ItemID.Valid && r.JoinedProductID.Valid }

// item converts the row into a publicItem. Stock is computed from the stock
// limit; callers fill it from the catalog when there is no limit.
func (r *itemRow) item() publicItem {
	stock := 0
	if r.StockLimit.Valid {
		stock = int(r.StockLimit.Int32 - r.Sold.Int32)
		if stock < 0 {
			stock = 0
		}
	}
	return publicItem{ID: cart.UUIDString(r.ItemID), ProductID: cart.UUIDString(r.ProductID), Title: r.Title.String, Slug: r.ProductSlug.String, OriginalPrice: r.Original.Int64, SalePrice: r.Sale.Int64, DiscountBps: r.Discount.Int32, Stock: stock, StockLimit: nullableInt4(r.StockLimit), SoldCount: r.Sold.Int32, Thumbnail: nullableText(r.Thumbnail)}
}

// catalogStock sums variant stock for a product of the given tenant. Errors
// are ignored (stock 0), as before.
func (h *Handler) catalogStock(ctx context.Context, productID, tenantID pgtype.UUID) int {
	stock := 0
	_ = h.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(v.stock),0)::int
		FROM product_variants v
		JOIN products p ON p.id=v.product_id
		WHERE v.product_id=$1 AND p.tenant_id=$2`, productID, tenantID).Scan(&stock)
	if stock < 0 {
		stock = 0
	}
	return stock
}

func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		notConfigured(r.Context(), w)
		return
	}
	tenantID, ok := tenantUUID(r)
	if !ok {
		common.JSONError(w, 400, "MISSING_TENANT", "tenant is required", nil)
		return
	}

	// Parse pagination
	page := 1
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// Get total count
	var total int64
	err := h.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM flash_sale_campaigns WHERE tenant_id=$1`, tenantID).Scan(&total)
	if err != nil {
		internalError(r.Context(), w, err, "failed to count flash sales")
		return
	}

	// Get paginated results
	rows, err := h.Pool.Query(r.Context(), `
		SELECT c.id,c.name,c.slug,c.status,c.starts_at,c.ends_at,c.created_at,c.updated_at,
		       i.id,i.product_id,p.id,p.title,p.slug,p.price,i.sale_price,
		       i.stock_limit,i.sold_count,p.thumbnail,
		       CASE WHEN p.price > 0 THEN ((p.price-i.sale_price)*10000/p.price)::int ELSE 0 END
		FROM flash_sale_campaigns c
		LEFT JOIN flash_sale_items i ON i.campaign_id=c.id
		LEFT JOIN products p ON p.id=i.product_id AND p.tenant_id=c.tenant_id
		WHERE c.tenant_id=$1
		ORDER BY c.created_at DESC, i.created_at ASC
		LIMIT $2 OFFSET $3`, tenantID, limit, offset)
	if err != nil {
		internalError(r.Context(), w, err, "failed to list flash sales")
		return
	}
	defer rows.Close()

	campaigns := map[string]*map[string]any{}
	ordered := make([]*map[string]any, 0)
	for rows.Next() {
		var cID pgtype.UUID
		var name, slug, status string
		var startsAt, endsAt, createdAt, updatedAt time.Time
		var row itemRow
		if err := rows.Scan(append([]any{&cID, &name, &slug, &status, &startsAt, &endsAt, &createdAt, &updatedAt}, row.targets()...)...); err != nil {
			internalError(r.Context(), w, err, "failed to read flash sales")
			return
		}
		key := cart.UUIDString(cID)
		current, exists := campaigns[key]
		if !exists {
			body := map[string]any{"id": key, "name": name, "slug": slug, "status": status, "startsAt": startsAt, "endsAt": endsAt, "createdAt": createdAt, "updatedAt": updatedAt, "items": []publicItem{}}
			campaigns[key] = &body
			ordered = append(ordered, &body)
			current = &body
		}
		if row.visible() {
			item := row.item()
			if !row.StockLimit.Valid {
				item.Stock = h.catalogStock(r.Context(), row.ProductID, tenantID)
			}
			items := (*current)["items"].([]publicItem)
			items = append(items, item)
			(*current)["items"] = items
		}
	}
	if err := rows.Err(); err != nil {
		internalError(r.Context(), w, err, "failed to read flash sales")
		return
	}
	result := make([]map[string]any, 0, len(ordered))
	for _, item := range ordered {
		result = append(result, *item)
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data":       result,
		"pagination": common.Pagination{Page: page, PerPage: limit, TotalItems: int(total)},
	})
}

func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		notConfigured(r.Context(), w)
		return
	}
	tenantID, ok := tenantUUID(r)
	if !ok {
		common.JSONError(w, 400, "MISSING_TENANT", "tenant is required", nil)
		return
	}
	var input campaignInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", decodeError(err), nil)
		return
	}
	if err := validateInput(input); err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", err.Error(), nil)
		return
	}
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		internalError(r.Context(), w, err, "failed to begin campaign")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	checks := make([]productPrice, 0, len(input.Items))
	for _, item := range input.Items {
		productID, _ := cart.ToUUID(item.ProductID)
		checks = append(checks, productPrice{ProductID: productID, SalePrice: item.SalePrice})
	}
	if err := validateItemProducts(ctx, tx, tenantID, checks); err != nil {
		writeCampaignError(ctx, w, err, "failed to save campaign")
		return
	}
	var campaignID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO flash_sale_campaigns(tenant_id,name,slug,status,starts_at,ends_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, tenantID, strings.TrimSpace(input.Name), strings.TrimSpace(input.Slug), normalizedStatus(input.Status), input.StartsAt, input.EndsAt).Scan(&campaignID)
	if err != nil {
		writeCampaignError(ctx, w, err, "failed to save campaign")
		return
	}
	if err := insertItems(ctx, tx, campaignID, input.Items); err != nil {
		writeCampaignError(ctx, w, err, "failed to save campaign")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		internalError(r.Context(), w, err, "failed to save campaign")
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": map[string]any{"id": cart.UUIDString(campaignID)}})
}

// AdminUpdate applies a partial or full campaign update in one transaction.
// Absent fields stay unchanged; `items`, when present, replaces the item set
// keyed by item id. A bare `{"status": ...}` body remains valid.
func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Pool == nil {
		notConfigured(r.Context(), w)
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
	var input campaignUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", decodeError(err), nil)
		return
	}
	ctx := r.Context()
	updated, err := h.applyUpdate(ctx, campaignID, tenantID, input)
	if err != nil {
		writeCampaignError(ctx, w, err, "failed to update campaign")
		return
	}
	campaign, err := h.loadCampaign(ctx, campaignID, tenantID)
	if err != nil {
		// The update is committed; never answer 5xx from here.
		zerolog.Ctx(ctx).Warn().Err(err).Str("campaign_id", cart.UUIDString(campaignID)).Msg("reload flash sale after update failed")
		common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"id": cart.UUIDString(updated.ID), "status": updated.Status, "updatedAt": updated.UpdatedAt}})
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": campaign})
}

// updatedCampaign is the RETURNING triple of the campaign UPDATE.
type updatedCampaign struct {
	ID        pgtype.UUID
	Status    string
	UpdatedAt time.Time
}

// errCampaignNotFound marks a campaign that does not exist for the tenant.
var errCampaignNotFound = errors.New("campaign not found")

// applyUpdate runs the transactional part of AdminUpdate and commits it.
func (h *Handler) applyUpdate(ctx context.Context, campaignID, tenantID pgtype.UUID, input campaignUpdateInput) (updatedCampaign, error) {
	var out updatedCampaign
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin campaign update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		return out, fmt.Errorf("set lock timeout: %w", err)
	}

	var current existingCampaign
	err = tx.QueryRow(ctx, `SELECT name, slug, status, starts_at, ends_at FROM flash_sale_campaigns WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, campaignID, tenantID).
		Scan(&current.Name, &current.Slug, &current.Status, &current.StartsAt, &current.EndsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errCampaignNotFound
	}
	if err != nil {
		return out, fmt.Errorf("locking campaign: %w", err)
	}

	resolved, err := validateUpdateInput(input, current, time.Now())
	if err != nil {
		return out, err
	}

	if input.Items != nil {
		if err := applyItemChanges(ctx, tx, campaignID, tenantID, *input.Items); err != nil {
			return out, err
		}
	}

	err = tx.QueryRow(ctx, `
		UPDATE flash_sale_campaigns
		SET name=$1, slug=$2, status=$3, starts_at=$4, ends_at=$5, updated_at=now()
		WHERE id=$6 AND tenant_id=$7
		RETURNING id, status, updated_at`,
		resolved.Name, resolved.Slug, resolved.Status, resolved.StartsAt, resolved.EndsAt, campaignID, tenantID).
		Scan(&out.ID, &out.Status, &out.UpdatedAt)
	if err != nil {
		return out, fmt.Errorf("updating campaign: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("committing campaign update: %w", err)
	}
	return out, nil
}

// applyItemChanges locks the campaign items, plans the diff against the
// requested set, validates products and executes deletes, ordered updates and
// inserts.
func applyItemChanges(ctx context.Context, tx pgx.Tx, campaignID, tenantID pgtype.UUID, requested []updateItemInput) error {
	rows, err := tx.Query(ctx, `SELECT id, product_id, sale_price, stock_limit, sold_count FROM flash_sale_items WHERE campaign_id=$1 ORDER BY id FOR UPDATE`, campaignID)
	if err != nil {
		return fmt.Errorf("locking items: %w", err)
	}
	existing, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (existingItem, error) {
		var it existingItem
		err := row.Scan(&it.ID, &it.ProductID, &it.SalePrice, &it.StockLimit, &it.SoldCount)
		return it, err
	})
	if err != nil {
		return fmt.Errorf("reading items: %w", err)
	}

	plan, err := planItemChanges(existing, requested)
	if err != nil {
		return err
	}

	checks := make([]productPrice, 0, len(plan.Update)+len(plan.Insert))
	for _, u := range plan.Update {
		checks = append(checks, productPrice{ItemID: u.ItemID, ProductID: u.ProductID, SalePrice: u.SalePrice})
	}
	for _, in := range plan.Insert {
		productID, _ := cart.ToUUID(in.ProductID)
		checks = append(checks, productPrice{ProductID: productID, SalePrice: in.SalePrice})
	}
	if err := validateItemProducts(ctx, tx, tenantID, checks); err != nil {
		return err
	}

	if len(plan.Delete) > 0 {
		ids := make([]pgtype.UUID, 0, len(plan.Delete))
		for _, d := range plan.Delete {
			ids = append(ids, d.ID)
		}
		// D5: items being removed have sold_count = 0 under the row lock, so
		// only RELEASED reservation rows can reference them.
		if _, err := tx.Exec(ctx, `DELETE FROM flash_sale_order_items WHERE flash_sale_item_id = ANY($1) AND status = 'RELEASED'`, ids); err != nil {
			return fmt.Errorf("deleting released reservations: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM flash_sale_items WHERE id = ANY($1) AND campaign_id = $2`, ids, campaignID); err != nil {
			return fmt.Errorf("deleting items: %w", err)
		}
	}
	for _, u := range plan.Update {
		if _, err := tx.Exec(ctx, `UPDATE flash_sale_items SET product_id=$1, sale_price=$2, stock_limit=$3 WHERE id=$4 AND campaign_id=$5`, u.ProductID, u.SalePrice, u.StockLimit, u.ItemID, campaignID); err != nil {
			return fmt.Errorf("updating item: %w", err)
		}
	}
	inserts := make([]itemInput, 0, len(plan.Insert))
	for _, in := range plan.Insert {
		inserts = append(inserts, itemInput{ProductID: in.ProductID, SalePrice: in.SalePrice, StockLimit: in.StockLimit})
	}
	return insertItems(ctx, tx, campaignID, inserts)
}

// decodeError turns a JSON decode error into a client-facing message.
func decodeError(err error) string {
	if errors.As(err, new(*time.ParseError)) {
		return "startsAt and endsAt must be RFC 3339 timestamps with a timezone (e.g. 2026-12-01T00:00:00Z)"
	}
	return "invalid payload"
}

// writeCampaignError maps errors from create/update to HTTP responses. Raw
// database messages are never sent to the client.
func writeCampaignError(ctx context.Context, w http.ResponseWriter, err error, fallback string) {
	if errors.Is(err, errCampaignNotFound) {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found", nil)
		return
	}
	var verr *validationError
	if errors.As(err, &verr) {
		status := http.StatusBadRequest
		code := "BAD_REQUEST"
		if errors.Is(err, errItemHasSales) {
			status, code = http.StatusConflict, "CONFLICT"
		}
		common.JSONError(w, status, code, verr.Message, verr.details())
		return
	}
	status, code, message := mapDBError(err, fallback)
	if status >= http.StatusInternalServerError {
		internalError(ctx, w, err, message)
		return
	}
	common.JSONError(w, status, code, message, nil)
}

// internalError logs a 5xx with its cause on the request logger and writes a
// generic body. err is logged, never sent to the client.
func internalError(ctx context.Context, w http.ResponseWriter, err error, message string) {
	evt := zerolog.Ctx(ctx).Error()
	if err != nil {
		evt = evt.Err(err)
	}
	evt.Str("response_message", message).Msg("campaign request failed")
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", message, nil)
}

// notConfigured reports a handler without a database pool, so a misconfigured
// deploy shows up in the logs.
func notConfigured(ctx context.Context, w http.ResponseWriter) {
	logger := zerolog.Ctx(ctx).With().Str("reason", "pool not configured").Logger()
	internalError(logger.WithContext(ctx), w, nil, "campaign service not configured")
}

// mapDBError maps Postgres errors by constraint name first, then by SQLSTATE.
func mapDBError(err error, fallback string) (int, string, string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "flash_sale_campaigns_tenant_id_slug_key":
			return http.StatusConflict, "CONFLICT", "campaign slug already exists"
		case "flash_sale_items_campaign_id_product_id_key":
			return http.StatusConflict, "CONFLICT", "product is already in this campaign"
		case "flash_sale_order_items_flash_sale_item_id_fkey":
			return http.StatusConflict, "CONFLICT", "item has active reservations"
		}
		switch pgErr.Code {
		case "23514":
			return http.StatusBadRequest, "BAD_REQUEST", "campaign violates a constraint"
		case "55P03", "40P01":
			// lock_not_available / deadlock_detected: a checkout can hold item
			// locks in another order than this transaction.
			return http.StatusConflict, "CONFLICT", "campaign is busy, retry"
		}
	}
	return http.StatusInternalServerError, "INTERNAL", fallback
}

// campaignUpdateInput distinguishes absent fields (nil) from provided ones.
type campaignUpdateInput struct {
	Name     *string            `json:"name"`
	Slug     *string            `json:"slug"`
	Status   *string            `json:"status"`
	StartsAt *time.Time         `json:"startsAt"`
	EndsAt   *time.Time         `json:"endsAt"`
	Items    *[]updateItemInput `json:"items"`
}

type updateItemInput struct {
	ID         *string `json:"id"`
	ProductID  string  `json:"productId"`
	SalePrice  int64   `json:"salePrice"`
	StockLimit *int32  `json:"stockLimit"`
}

type existingItem struct {
	ID, ProductID pgtype.UUID
	SalePrice     int64
	StockLimit    pgtype.Int4
	SoldCount     int32
}

type existingCampaign struct {
	Name, Slug, Status string
	StartsAt, EndsAt   time.Time
}

// plannedUpdate is one UPDATE of an existing item, in execution order.
type plannedUpdate struct {
	ItemID           pgtype.UUID
	CurrentProductID pgtype.UUID
	ProductID        pgtype.UUID
	SalePrice        int64
	StockLimit       *int32
}

type itemPlan struct {
	Delete []existingItem
	Update []plannedUpdate
	Insert []updateItemInput
}

// errItemHasSales is wrapped by the validation error returned when an item
// with sales would be removed; it maps to 409.
var errItemHasSales = errors.New("item has sales and cannot be removed")

// validationError is a client error with optional structured details.
type validationError struct {
	Message      string
	Field        string
	ItemID       string
	ProductID    string
	SalePrice    *int64
	ProductPrice *int64
	err          error
}

func (e *validationError) Error() string { return e.Message }
func (e *validationError) Unwrap() error { return e.err }

func (e *validationError) details() map[string]any {
	d := map[string]any{}
	if e.Field != "" {
		d["field"] = e.Field
	}
	if e.ItemID != "" {
		d["itemId"] = e.ItemID
	}
	if e.ProductID != "" {
		d["productId"] = e.ProductID
	}
	if e.SalePrice != nil {
		d["salePrice"] = *e.SalePrice
	}
	if e.ProductPrice != nil {
		d["productPrice"] = *e.ProductPrice
	}
	if len(d) == 0 {
		return nil
	}
	return d
}

var campaignStatuses = map[string]bool{"DRAFT": true, "SCHEDULED": true, "ACTIVE": true, "ENDED": true}

// validateUpdateInput checks the top-level fields and merges them over the
// current campaign, returning the values to write.
func validateUpdateInput(in campaignUpdateInput, current existingCampaign, now time.Time) (existingCampaign, error) {
	resolved := current
	if in.Name == nil && in.Slug == nil && in.Status == nil && in.StartsAt == nil && in.EndsAt == nil && in.Items == nil {
		return resolved, &validationError{Message: "no updatable fields provided"}
	}
	if in.Name != nil {
		resolved.Name = strings.TrimSpace(*in.Name)
		if resolved.Name == "" {
			return current, &validationError{Message: "name cannot be empty", Field: "name"}
		}
	}
	if in.Slug != nil {
		resolved.Slug = strings.TrimSpace(*in.Slug)
		if resolved.Slug == "" {
			return current, &validationError{Message: "slug cannot be empty", Field: "slug"}
		}
	}
	if in.Status != nil {
		// Checked before normalizedStatus, which would coerce "" to DRAFT.
		if strings.TrimSpace(*in.Status) == "" {
			return current, &validationError{Message: "unsupported campaign status", Field: "status"}
		}
		resolved.Status = normalizedStatus(*in.Status)
		if !campaignStatuses[resolved.Status] {
			return current, &validationError{Message: "unsupported campaign status", Field: "status"}
		}
	}
	if in.StartsAt != nil {
		resolved.StartsAt = *in.StartsAt
	}
	if in.EndsAt != nil {
		resolved.EndsAt = *in.EndsAt
	}
	if !resolved.EndsAt.After(resolved.StartsAt) {
		return current, &validationError{Message: "startsAt and endsAt must define a valid range", Field: "endsAt"}
	}
	if (resolved.Status == "SCHEDULED" || resolved.Status == "ACTIVE") && !resolved.EndsAt.After(now) {
		return current, &validationError{Message: "endsAt must be in the future for a scheduled or active campaign", Field: "endsAt"}
	}
	return resolved, nil
}

// planItemChanges diffs the existing items against the requested full item
// set. Deletes run first, then updates in the returned order, then inserts.
// UNIQUE(campaign_id, product_id) is not deferrable, so an update that moves
// onto a product still held by another updated item runs after that item.
func planItemChanges(existing []existingItem, requested []updateItemInput) (itemPlan, error) {
	var plan itemPlan
	if len(requested) == 0 {
		return plan, &validationError{Message: "at least one item is required", Field: "items"}
	}
	byID := make(map[[16]byte]existingItem, len(existing))
	for _, it := range existing {
		byID[it.ID.Bytes] = it
	}
	seenProducts := map[[16]byte]bool{}
	seenIDs := map[[16]byte]bool{}
	for _, req := range requested {
		productID, err := cart.ToUUID(req.ProductID)
		if err != nil {
			return plan, &validationError{Message: "item productId is invalid", Field: "productId", ProductID: req.ProductID}
		}
		pid := cart.UUIDString(productID)
		if seenProducts[productID.Bytes] {
			return plan, &validationError{Message: "duplicate productId in items", Field: "productId", ProductID: pid}
		}
		seenProducts[productID.Bytes] = true
		if req.SalePrice < 0 {
			return plan, &validationError{Message: "salePrice cannot be negative", Field: "salePrice", ProductID: pid}
		}
		if req.StockLimit != nil && *req.StockLimit < 1 {
			return plan, &validationError{Message: "stockLimit must be at least 1 or null", Field: "stockLimit", ProductID: pid}
		}
		if req.ID == nil {
			plan.Insert = append(plan.Insert, req)
			continue
		}
		itemID, err := cart.ToUUID(*req.ID)
		if err != nil {
			return plan, &validationError{Message: "item id is invalid", Field: "id", ItemID: *req.ID}
		}
		iid := cart.UUIDString(itemID)
		if seenIDs[itemID.Bytes] {
			return plan, &validationError{Message: "duplicate item id in items", Field: "id", ItemID: iid}
		}
		seenIDs[itemID.Bytes] = true
		cur, ok := byID[itemID.Bytes]
		if !ok {
			return plan, &validationError{Message: fmt.Sprintf("item %s does not belong to this campaign", iid), Field: "id", ItemID: iid}
		}
		if req.StockLimit != nil && *req.StockLimit < cur.SoldCount {
			return plan, &validationError{Message: "stockLimit cannot be below soldCount", Field: "stockLimit", ItemID: iid, ProductID: pid}
		}
		if cur.SoldCount > 0 && productID.Bytes != cur.ProductID.Bytes {
			return plan, &validationError{Message: "cannot change the product of an item that already has sales", Field: "productId", ItemID: iid, ProductID: pid}
		}
		plan.Update = append(plan.Update, plannedUpdate{ItemID: itemID, CurrentProductID: cur.ProductID, ProductID: productID, SalePrice: req.SalePrice, StockLimit: req.StockLimit})
	}
	for _, it := range existing {
		if seenIDs[it.ID.Bytes] {
			continue
		}
		if it.SoldCount > 0 {
			return plan, &validationError{Message: "item has sales and cannot be removed", ItemID: cart.UUIDString(it.ID), ProductID: cart.UUIDString(it.ProductID), err: errItemHasSales}
		}
		plan.Delete = append(plan.Delete, it)
	}
	ordered, err := orderUpdates(plan.Update)
	if err != nil {
		return plan, err
	}
	plan.Update = ordered
	return plan, nil
}

// orderUpdates sorts updates so that an item vacates its current product
// before another item moves onto it. Each update depends on at most one other
// (the updated item currently holding its target product), so the graph is a
// set of chains; a cycle means products are swapped.
func orderUpdates(updates []plannedUpdate) ([]plannedUpdate, error) {
	holder := make(map[[16]byte]int, len(updates)) // current product -> update index
	for i, u := range updates {
		holder[u.CurrentProductID.Bytes] = i
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make([]int, len(updates))
	ordered := make([]plannedUpdate, 0, len(updates))
	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case done:
			return nil
		case visiting:
			return &validationError{Message: "swapping products between existing items in one request is not supported; remove one and add it again", Field: "items", ItemID: cart.UUIDString(updates[i].ItemID)}
		}
		state[i] = visiting
		if dep, ok := holder[updates[i].ProductID.Bytes]; ok && dep != i {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[i] = done
		ordered = append(ordered, updates[i])
		return nil
	}
	for i := range updates {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// productPrice is one item's product reference to validate against the tenant
// catalog. ItemID is set for updates of existing items.
type productPrice struct {
	ItemID    pgtype.UUID
	ProductID pgtype.UUID
	SalePrice int64
}

// validateItemProducts checks in one query that every product belongs to the
// tenant and that each sale price is below the product's current price.
// Products of other tenants are reported as invalid, without revealing that
// they exist.
func validateItemProducts(ctx context.Context, tx pgx.Tx, tenantID pgtype.UUID, items []productPrice) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ProductID)
	}
	rows, err := tx.Query(ctx, `SELECT id, price FROM products WHERE tenant_id=$1 AND id = ANY($2::uuid[])`, tenantID, ids)
	if err != nil {
		return fmt.Errorf("loading products: %w", err)
	}
	prices := map[[16]byte]int64{}
	for rows.Next() {
		var id pgtype.UUID
		var price int64
		if err := rows.Scan(&id, &price); err != nil {
			rows.Close()
			return fmt.Errorf("reading products: %w", err)
		}
		prices[id.Bytes] = price
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading products: %w", err)
	}
	return checkItemPrices(items, prices)
}

// checkItemPrices applies the tenant-ownership and price rules to the prices
// found for the tenant.
func checkItemPrices(items []productPrice, prices map[[16]byte]int64) error {
	for _, it := range items {
		price, ok := prices[it.ProductID.Bytes]
		if !ok {
			return &validationError{Message: "item productId is invalid", Field: "productId", ItemID: cart.UUIDString(it.ItemID), ProductID: cart.UUIDString(it.ProductID)}
		}
		if it.SalePrice >= price {
			sale, productPrice := it.SalePrice, price
			return &validationError{Message: "salePrice must be lower than the product price", Field: "salePrice", ItemID: cart.UUIDString(it.ItemID), ProductID: cart.UUIDString(it.ProductID), SalePrice: &sale, ProductPrice: &productPrice}
		}
	}
	return nil
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
	seen := make(map[[16]byte]bool, len(input.Items))
	for _, item := range input.Items {
		productID, err := cart.ToUUID(item.ProductID)
		if err != nil {
			return errors.New("item productId is invalid")
		}
		if seen[productID.Bytes] {
			return errors.New("duplicate productId in items")
		}
		seen[productID.Bytes] = true
		if item.SalePrice < 0 {
			return errors.New("salePrice cannot be negative")
		}
	}
	return nil
}

func insertItems(ctx context.Context, tx pgx.Tx, campaignID pgtype.UUID, items []itemInput) error {
	for _, item := range items {
		productID, _ := cart.ToUUID(item.ProductID)
		if _, err := tx.Exec(ctx, `INSERT INTO flash_sale_items(campaign_id,product_id,sale_price,stock_limit) VALUES($1,$2,$3,$4)`, campaignID, productID, item.SalePrice, item.StockLimit); err != nil {
			return fmt.Errorf("inserting item: %w", err)
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
