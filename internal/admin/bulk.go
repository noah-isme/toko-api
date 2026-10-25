package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	bulkMaxRows  = 500
	bulkMaxBytes = 1 << 20

	applyToBoth     = "both"
	applyToProduct  = "product"
	applyToVariants = "variants"
)

// Row error codes shared by bulk endpoints.
const (
	codeMissing        = "MISSING"
	codeAmbiguous      = "AMBIGUOUS"
	codeInvalidUUID    = "INVALID_UUID"
	codeNegative       = "NEGATIVE"
	codeNotFound       = "NOT_FOUND"
	codeDuplicate      = "DUPLICATE"
	codeInvalidStatus  = "INVALID_STATUS"
	codeInvalidApplyTo = "INVALID_APPLY_TO"
	codeNegativeResult = "NEGATIVE_RESULT"
	codeOutOfRange     = "OUT_OF_RANGE"
	codeNotAllowed     = "NOT_ALLOWED"
)

// bulkRowError is one entry of the 422 details.rows[] list. Index is the
// zero-based position in the request's items array.
type bulkRowError struct {
	Index   int    `json:"index"`
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type bulkErrors []bulkRowError

func (e *bulkErrors) add(index int, field, code, message string) {
	*e = append(*e, bulkRowError{Index: index, Field: field, Code: code, Message: message})
}

// optionalNullableInt64 distinguishes an absent key, an explicit null and a value.
type optionalNullableInt64 struct {
	Set   bool
	Value *int64
}

func (o *optionalNullableInt64) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}
	var v int64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// decodeBulkItems reads {"items": [...]} with a 1 MiB cap and unknown-field
// rejection, and enforces the 1..500 item bounds. It writes the error response
// itself and reports whether decoding succeeded.
func decodeBulkItems[T any](w http.ResponseWriter, r *http.Request) ([]T, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, bulkMaxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Items []T `json:"items"`
	}
	if err := dec.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			common.JSONError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds 1 MiB", nil)
			return nil, false
		}
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload: "+err.Error(), nil)
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload: trailing data", nil)
		return nil, false
	}
	if len(body.Items) == 0 {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "items must not be empty", nil)
		return nil, false
	}
	if len(body.Items) > bulkMaxRows {
		common.JSONError(w, http.StatusBadRequest, "TOO_MANY_ROWS", fmt.Sprintf("at most %d items per request", bulkMaxRows), map[string]any{"max": bulkMaxRows, "got": len(body.Items)})
		return nil, false
	}
	return body.Items, true
}

func writeBulkValidation(w http.ResponseWriter, errs bulkErrors) {
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Index < errs[j].Index })
	common.JSONError(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "one or more items are invalid; nothing was changed", map[string]any{"rows": errs})
}

// parseItemTarget validates the productId/variantId pair of a bulk item.
// allowVariant=false means only productId is accepted.
func parseItemTarget(errs *bulkErrors, index int, productID, variantID *string, allowVariant bool) (pgtype.UUID, pgtype.UUID, bool) {
	hasProduct := productID != nil && strings.TrimSpace(*productID) != ""
	hasVariant := variantID != nil && strings.TrimSpace(*variantID) != ""
	switch {
	case hasProduct && hasVariant:
		errs.add(index, "productId", codeAmbiguous, "send either productId or variantId, not both")
		return pgtype.UUID{}, pgtype.UUID{}, false
	case !hasProduct && !hasVariant:
		field, msg := "productId", "productId or variantId is required"
		if !allowVariant {
			msg = "productId is required"
		}
		errs.add(index, field, codeMissing, msg)
		return pgtype.UUID{}, pgtype.UUID{}, false
	case hasVariant && !allowVariant:
		errs.add(index, "variantId", codeNotAllowed, "variantId is not accepted here")
		return pgtype.UUID{}, pgtype.UUID{}, false
	case hasProduct:
		id, err := parsePGUUID(*productID)
		if err != nil {
			errs.add(index, "productId", codeInvalidUUID, "productId must be a UUID")
			return pgtype.UUID{}, pgtype.UUID{}, false
		}
		return id, pgtype.UUID{}, true
	default:
		id, err := parsePGUUID(*variantID)
		if err != nil {
			errs.add(index, "variantId", codeInvalidUUID, "variantId must be a UUID")
			return pgtype.UUID{}, pgtype.UUID{}, false
		}
		return pgtype.UUID{}, id, true
	}
}

// ---------------------------------------------------------------------------
// Bulk price

type bulkPriceItem struct {
	ProductID *string               `json:"productId"`
	VariantID *string               `json:"variantId"`
	Price     *int64                `json:"price"`
	CompareAt optionalNullableInt64 `json:"compareAt"`
	ApplyTo   *string               `json:"applyTo"`
}

type bulkPriceTarget struct {
	index     int
	productID pgtype.UUID
	variantID pgtype.UUID
	price     int64
	compareAt optionalNullableInt64
	applyTo   string
}

func validateBulkPrice(items []bulkPriceItem) ([]bulkPriceTarget, bulkErrors) {
	var errs bulkErrors
	targets := make([]bulkPriceTarget, 0, len(items))
	seenProducts := map[[16]byte]int{}
	seenVariants := map[[16]byte]int{}
	for i, item := range items {
		productID, variantID, ok := parseItemTarget(&errs, i, item.ProductID, item.VariantID, true)
		valid := ok
		if item.Price == nil {
			errs.add(i, "price", codeMissing, "price is required")
			valid = false
		} else if *item.Price < 0 {
			errs.add(i, "price", codeNegative, "price must be zero or greater")
			valid = false
		}
		if item.CompareAt.Set && item.CompareAt.Value != nil && *item.CompareAt.Value < 0 {
			errs.add(i, "compareAt", codeNegative, "compareAt must be zero or greater")
			valid = false
		}
		applyTo := applyToBoth
		if item.ApplyTo != nil {
			applyTo = strings.ToLower(strings.TrimSpace(*item.ApplyTo))
			if applyTo != applyToBoth && applyTo != applyToProduct && applyTo != applyToVariants {
				errs.add(i, "applyTo", codeInvalidApplyTo, "applyTo must be both, product or variants")
				valid = false
			}
		}
		if ok && variantID.Valid {
			if item.ApplyTo != nil {
				errs.add(i, "applyTo", codeNotAllowed, "applyTo only applies to productId items")
				valid = false
			}
			if item.CompareAt.Set {
				errs.add(i, "compareAt", codeNotAllowed, "compareAt only applies to productId items")
				valid = false
			}
		}
		if ok && productID.Valid && applyTo == applyToVariants && item.CompareAt.Set {
			errs.add(i, "compareAt", codeNotAllowed, "compareAt is a product field; applyTo=variants leaves the product untouched")
			valid = false
		}
		if ok {
			if productID.Valid {
				if first, dup := seenProducts[productID.Bytes]; dup {
					errs.add(i, "productId", codeDuplicate, fmt.Sprintf("product already targeted by item %d", first))
					valid = false
				} else {
					seenProducts[productID.Bytes] = i
				}
			} else {
				if first, dup := seenVariants[variantID.Bytes]; dup {
					errs.add(i, "variantId", codeDuplicate, fmt.Sprintf("variant already targeted by item %d", first))
					valid = false
				} else {
					seenVariants[variantID.Bytes] = i
				}
			}
		}
		if valid {
			targets = append(targets, bulkPriceTarget{index: i, productID: productID, variantID: variantID, price: *item.Price, compareAt: item.CompareAt, applyTo: applyTo})
		}
	}
	return targets, errs
}

type bulkAuditItem struct {
	ProductID string                    `json:"productId,omitempty"`
	VariantID string                    `json:"variantId,omitempty"`
	SKU       string                    `json:"sku,omitempty"`
	Slug      string                    `json:"slug,omitempty"`
	Changes   map[string]map[string]any `json:"changes"`
}

func changeMap(changes []catalog.ProductChange) map[string]map[string]any {
	out := make(map[string]map[string]any, len(changes))
	for _, c := range changes {
		out[c.Field] = map[string]any{"from": c.Old, "to": c.New}
	}
	return out
}

// BulkPrice handles POST /api/v1/admin/products/bulk/price.
func (h *CatalogHandler) BulkPrice(w http.ResponseWriter, r *http.Request) {
	items, ok := decodeBulkItems[bulkPriceItem](w, r)
	if !ok {
		return
	}
	targets, errs := validateBulkPrice(items)
	if len(errs) > 0 {
		writeBulkValidation(w, errs)
		return
	}
	if h == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "bulk operations not configured", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	tx, err := beginCatalogTx(ctx, h.Pool)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to start bulk update", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)

	var productIDs, variantIDs, cascadeProductIDs []pgtype.UUID
	for _, t := range targets {
		if t.productID.Valid {
			productIDs = append(productIDs, t.productID)
			if t.applyTo != applyToProduct {
				cascadeProductIDs = append(cascadeProductIDs, t.productID)
			}
		} else {
			variantIDs = append(variantIDs, t.variantID)
		}
	}
	products := map[[16]byte]dbgen.AdminGetProductsByIDsForUpdateRow{}
	if len(productIDs) > 0 {
		rows, err := q.AdminGetProductsByIDsForUpdate(ctx, dbgen.AdminGetProductsByIDsForUpdateParams{Ids: productIDs, TenantID: tenantID})
		if err != nil {
			writeTxError(w, err, "failed to lock products")
			return
		}
		for _, row := range rows {
			products[row.ID.Bytes] = row
		}
	}
	variants := map[[16]byte]dbgen.AdminLockVariantsByIDsOrProductsRow{}
	variantsByProduct := map[[16]byte][]dbgen.AdminLockVariantsByIDsOrProductsRow{}
	if len(variantIDs) > 0 || len(cascadeProductIDs) > 0 {
		rows, err := q.AdminLockVariantsByIDsOrProducts(ctx, dbgen.AdminLockVariantsByIDsOrProductsParams{Ids: variantIDs, ProductIds: cascadeProductIDs, TenantID: tenantID})
		if err != nil {
			writeTxError(w, err, "failed to lock variants")
			return
		}
		for _, row := range rows {
			variants[row.ID.Bytes] = row
			variantsByProduct[row.ProductID.Bytes] = append(variantsByProduct[row.ProductID.Bytes], row)
		}
	}
	cascaded := map[[16]byte]bool{}
	for _, id := range cascadeProductIDs {
		cascaded[id.Bytes] = true
	}
	for _, t := range targets {
		if t.productID.Valid {
			if _, found := products[t.productID.Bytes]; !found {
				errs.add(t.index, "productId", codeNotFound, "product not found")
			}
			continue
		}
		v, found := variants[t.variantID.Bytes]
		if !found {
			errs.add(t.index, "variantId", codeNotFound, "variant not found")
			continue
		}
		if cascaded[v.ProductID.Bytes] {
			errs.add(t.index, "variantId", codeDuplicate, "variant is already updated by a productId item for its product")
		}
	}
	if len(errs) > 0 {
		writeBulkValidation(w, errs)
		return
	}

	var allChanges []catalog.ProductChange
	auditItems := make([]bulkAuditItem, 0, len(targets))
	results := make([]map[string]any, 0, len(targets))
	updated := 0
	for _, t := range targets {
		var itemChanges []catalog.ProductChange
		if t.productID.Valid {
			p := products[t.productID.Bytes]
			newPrice, newCompareAt := p.Price, p.CompareAt
			if t.applyTo != applyToVariants {
				newPrice = t.price
				if t.compareAt.Set {
					newCompareAt = optionalInt64(t.compareAt.Value)
				}
				if newPrice != p.Price {
					itemChanges = append(itemChanges, catalog.ProductChange{ProductID: p.ID, Slug: p.Slug, Field: catalog.FieldPrice, Old: p.Price, New: newPrice})
				}
				if !int8Equal(newCompareAt, p.CompareAt) {
					itemChanges = append(itemChanges, catalog.ProductChange{ProductID: p.ID, Slug: p.Slug, Field: catalog.FieldCompareAt, Old: nullableInt64(p.CompareAt), New: nullableInt64(newCompareAt)})
				}
				if len(itemChanges) > 0 {
					if _, err := q.AdminBulkUpdateProductPrice(ctx, dbgen.AdminBulkUpdateProductPriceParams{
						Price: newPrice, SetCompareAt: t.compareAt.Set, CompareAt: newCompareAt, ID: p.ID, TenantID: tenantID,
					}); err != nil {
						writeTxError(w, err, "failed to update product price")
						return
					}
				}
			}
			variantsUpdated := 0
			if t.applyTo != applyToProduct {
				for _, v := range variantsByProduct[p.ID.Bytes] {
					if v.Price == t.price {
						continue
					}
					if _, err := q.AdminSetVariantPrice(ctx, dbgen.AdminSetVariantPriceParams{Price: t.price, ID: v.ID, TenantID: tenantID}); err != nil {
						writeTxError(w, err, "failed to update variant price")
						return
					}
					variantsUpdated++
					change := catalog.ProductChange{ProductID: p.ID, VariantID: v.ID, Slug: p.Slug, SKU: v.Sku.String, Field: catalog.FieldPrice, Old: v.Price, New: t.price}
					allChanges = append(allChanges, change)
					auditItems = append(auditItems, bulkAuditItem{ProductID: uuidString(p.ID), VariantID: uuidString(v.ID), SKU: v.Sku.String, Slug: p.Slug, Changes: changeMap([]catalog.ProductChange{change})})
				}
			}
			if len(itemChanges) > 0 {
				auditItems = append(auditItems, bulkAuditItem{ProductID: uuidString(p.ID), Slug: p.Slug, Changes: changeMap(itemChanges)})
			}
			if len(itemChanges) > 0 || variantsUpdated > 0 {
				updated++
			}
			allChanges = append(allChanges, itemChanges...)
			results = append(results, map[string]any{
				"productId": uuidString(p.ID), "slug": p.Slug, "price": newPrice, "compareAt": nullableInt64(newCompareAt),
				"applyTo": t.applyTo, "variantsUpdated": variantsUpdated,
			})
			continue
		}
		v := variants[t.variantID.Bytes]
		if v.Price != t.price {
			if _, err := q.AdminSetVariantPrice(ctx, dbgen.AdminSetVariantPriceParams{Price: t.price, ID: v.ID, TenantID: tenantID}); err != nil {
				writeTxError(w, err, "failed to update variant price")
				return
			}
			change := catalog.ProductChange{ProductID: v.ProductID, VariantID: v.ID, Slug: v.Slug, SKU: v.Sku.String, Field: catalog.FieldPrice, Old: v.Price, New: t.price}
			allChanges = append(allChanges, change)
			auditItems = append(auditItems, bulkAuditItem{ProductID: uuidString(v.ProductID), VariantID: uuidString(v.ID), SKU: v.Sku.String, Slug: v.Slug, Changes: changeMap([]catalog.ProductChange{change})})
			updated++
		}
		results = append(results, map[string]any{
			"variantId": uuidString(v.ID), "productId": uuidString(v.ProductID), "sku": nullableString(v.Sku), "price": t.price,
		})
	}
	h.commitBulk(w, r, tx, q, "catalog.bulk.price", len(targets), updated, auditItems, allChanges, results)
}

// commitBulk writes the explicit audit row inside the transaction, commits,
// then runs the shared side effects and writes the 200 response.
func (h *CatalogHandler) commitBulk(w http.ResponseWriter, r *http.Request, tx pgx.Tx, q *dbgen.Queries, action string, total, updated int, auditItems []bulkAuditItem, changes []catalog.ProductChange, results []map[string]any) {
	ctx := r.Context()
	if updated > 0 {
		metadata := map[string]any{"summary": map[string]int{"items": total, "updated": updated}, "items": auditItems}
		if err := h.recordCatalogAudit(ctx, q, r, action, "catalog_bulk", http.StatusOK, metadata); err != nil {
			writeTxError(w, err, "failed to write audit log")
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		writeTxError(w, err, "failed to commit bulk update")
		return
	}
	h.afterProductChanges(ctx, catalog.ChangeSourceBulk, changes)
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"updated": updated, "items": results}})
}

// ---------------------------------------------------------------------------
// Bulk stock

type bulkStockItem struct {
	VariantID *string `json:"variantId"`
	Stock     *int32  `json:"stock"`
	Delta     *int32  `json:"delta"`
}

type bulkStockTarget struct {
	index     int
	variantID pgtype.UUID
	stock     *int32
	delta     *int32
}

func validateBulkStock(items []bulkStockItem) ([]bulkStockTarget, bulkErrors) {
	var errs bulkErrors
	targets := make([]bulkStockTarget, 0, len(items))
	seen := map[[16]byte]int{}
	for i, item := range items {
		valid := true
		var variantID pgtype.UUID
		if item.VariantID == nil || strings.TrimSpace(*item.VariantID) == "" {
			errs.add(i, "variantId", codeMissing, "variantId is required")
			valid = false
		} else if id, err := parsePGUUID(*item.VariantID); err != nil {
			errs.add(i, "variantId", codeInvalidUUID, "variantId must be a UUID")
			valid = false
		} else {
			variantID = id
			if first, dup := seen[id.Bytes]; dup {
				errs.add(i, "variantId", codeDuplicate, fmt.Sprintf("variant already targeted by item %d", first))
				valid = false
			} else {
				seen[id.Bytes] = i
			}
		}
		switch {
		case item.Stock != nil && item.Delta != nil:
			errs.add(i, "stock", codeAmbiguous, "send either stock or delta, not both")
			valid = false
		case item.Stock == nil && item.Delta == nil:
			errs.add(i, "stock", codeMissing, "stock or delta is required")
			valid = false
		case item.Stock != nil && *item.Stock < 0:
			errs.add(i, "stock", codeNegative, "stock must be zero or greater")
			valid = false
		}
		if valid {
			targets = append(targets, bulkStockTarget{index: i, variantID: variantID, stock: item.Stock, delta: item.Delta})
		}
	}
	return targets, errs
}

// BulkStock handles POST /api/v1/admin/products/bulk/stock.
func (h *CatalogHandler) BulkStock(w http.ResponseWriter, r *http.Request) {
	items, ok := decodeBulkItems[bulkStockItem](w, r)
	if !ok {
		return
	}
	targets, errs := validateBulkStock(items)
	if len(errs) > 0 {
		writeBulkValidation(w, errs)
		return
	}
	if h == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "bulk operations not configured", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	tx, err := beginCatalogTx(ctx, h.Pool)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to start bulk update", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)

	ids := make([]pgtype.UUID, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.variantID)
	}
	// Products first (id order), then variants, matching import and bulk price
	// so concurrent catalog writers cannot deadlock. Checkout locks only
	// variants, so this adds no cycle with reservations.
	if _, err := q.AdminLockProductsOfVariants(ctx, dbgen.AdminLockProductsOfVariantsParams{TenantID: tenantID, Ids: ids}); err != nil {
		writeTxError(w, err, "failed to lock products")
		return
	}
	// Locks every target in id order; a concurrent checkout reservation on one
	// of them makes this wait (bounded by lock_timeout) and then read the
	// post-reservation stock, so deltas compose with reservations.
	rows, err := q.AdminLockVariantsByIDs(ctx, dbgen.AdminLockVariantsByIDsParams{Ids: ids, TenantID: tenantID})
	if err != nil {
		writeTxError(w, err, "failed to lock variants")
		return
	}
	locked := make(map[[16]byte]dbgen.AdminLockVariantsByIDsRow, len(rows))
	for _, row := range rows {
		locked[row.ID.Bytes] = row
	}
	for _, t := range targets {
		v, found := locked[t.variantID.Bytes]
		if !found {
			errs.add(t.index, "variantId", codeNotFound, "variant not found")
			continue
		}
		if t.delta != nil {
			switch next := int64(v.Stock) + int64(*t.delta); {
			case next < 0:
				errs.add(t.index, "delta", codeNegativeResult, fmt.Sprintf("stock %d %+d would be negative", v.Stock, *t.delta))
			case next > math.MaxInt32:
				errs.add(t.index, "delta", codeOutOfRange, fmt.Sprintf("stock %d %+d exceeds %d", v.Stock, *t.delta, math.MaxInt32))
			}
		}
	}
	if len(errs) > 0 {
		writeBulkValidation(w, errs)
		return
	}

	var changes []catalog.ProductChange
	auditItems := make([]bulkAuditItem, 0, len(targets))
	newStock := make(map[[16]byte]int32, len(targets))
	touched := map[[16]byte]pgtype.UUID{}
	var touchedOrder []pgtype.UUID
	updated := 0
	for _, t := range targets {
		v := locked[t.variantID.Bytes]
		stock := v.Stock
		switch {
		case t.stock != nil && *t.stock != v.Stock:
			row, err := q.AdminSetVariantStock(ctx, dbgen.AdminSetVariantStockParams{Stock: *t.stock, ID: v.ID, TenantID: tenantID})
			if err != nil {
				writeTxError(w, err, "failed to update stock")
				return
			}
			stock = row.Stock
		case t.delta != nil && *t.delta != 0:
			row, err := q.AdminAdjustVariantStock(ctx, dbgen.AdminAdjustVariantStockParams{Delta: *t.delta, ID: v.ID, TenantID: tenantID})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeBulkValidation(w, bulkErrors{{Index: t.index, Field: "delta", Code: codeNegativeResult, Message: "stock would be negative"}})
					return
				}
				writeTxError(w, err, "failed to update stock")
				return
			}
			stock = row.Stock
		}
		newStock[v.ID.Bytes] = stock
		if stock != v.Stock {
			updated++
			change := catalog.ProductChange{ProductID: v.ProductID, VariantID: v.ID, Slug: v.Slug, SKU: v.Sku.String, Field: catalog.FieldStock, Old: v.Stock, New: stock}
			changes = append(changes, change)
			auditItems = append(auditItems, bulkAuditItem{ProductID: uuidString(v.ProductID), VariantID: uuidString(v.ID), SKU: v.Sku.String, Slug: v.Slug, Changes: changeMap([]catalog.ProductChange{change})})
			if _, ok := touched[v.ProductID.Bytes]; !ok {
				touched[v.ProductID.Bytes] = v.ProductID
				touchedOrder = append(touchedOrder, v.ProductID)
			}
		}
	}
	inStock := map[[16]byte]bool{}
	for _, productID := range touchedOrder {
		flag, err := q.AdminRecomputeProductStockFlag(ctx, dbgen.AdminRecomputeProductStockFlagParams{ID: productID, TenantID: tenantID})
		if err != nil {
			writeTxError(w, err, "failed to update stock flag")
			return
		}
		inStock[productID.Bytes] = flag
	}
	results := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		v := locked[t.variantID.Bytes]
		item := map[string]any{"variantId": uuidString(v.ID), "productId": uuidString(v.ProductID), "sku": nullableString(v.Sku), "stock": newStock[v.ID.Bytes]}
		if flag, ok := inStock[v.ProductID.Bytes]; ok {
			item["inStock"] = flag
		} else {
			item["inStock"] = nil
		}
		results = append(results, item)
	}
	h.commitBulk(w, r, tx, q, "catalog.bulk.stock", len(targets), updated, auditItems, changes, results)
}

// ---------------------------------------------------------------------------
// Bulk status

type bulkStatusItem struct {
	ProductID *string `json:"productId"`
	Status    *string `json:"status"`
}

type bulkStatusTarget struct {
	index     int
	productID pgtype.UUID
	status    string
}

func validateBulkStatus(items []bulkStatusItem) ([]bulkStatusTarget, bulkErrors) {
	var errs bulkErrors
	targets := make([]bulkStatusTarget, 0, len(items))
	seen := map[[16]byte]int{}
	for i, item := range items {
		productID, _, ok := parseItemTarget(&errs, i, item.ProductID, nil, false)
		valid := ok
		var status string
		if item.Status == nil || strings.TrimSpace(*item.Status) == "" {
			errs.add(i, "status", codeMissing, "status is required")
			valid = false
		} else if parsed, err := parseProductStatus(*item.Status); err != nil {
			errs.add(i, "status", codeInvalidStatus, "status must be draft, published or archived")
			valid = false
		} else {
			status = parsed.String
		}
		if ok {
			if first, dup := seen[productID.Bytes]; dup {
				errs.add(i, "productId", codeDuplicate, fmt.Sprintf("product already targeted by item %d", first))
				valid = false
			} else {
				seen[productID.Bytes] = i
			}
		}
		if valid {
			targets = append(targets, bulkStatusTarget{index: i, productID: productID, status: status})
		}
	}
	return targets, errs
}

// BulkStatus handles POST /api/v1/admin/products/bulk/status.
func (h *CatalogHandler) BulkStatus(w http.ResponseWriter, r *http.Request) {
	items, ok := decodeBulkItems[bulkStatusItem](w, r)
	if !ok {
		return
	}
	targets, errs := validateBulkStatus(items)
	if len(errs) > 0 {
		writeBulkValidation(w, errs)
		return
	}
	if h == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "bulk operations not configured", nil)
		return
	}
	ctx := r.Context()
	tenantID := tenantIDFromContext(ctx)
	tx, err := beginCatalogTx(ctx, h.Pool)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to start bulk update", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)

	ids := make([]pgtype.UUID, 0, len(targets))
	for _, t := range targets {
		ids = append(ids, t.productID)
	}
	rows, err := q.AdminGetProductsByIDsForUpdate(ctx, dbgen.AdminGetProductsByIDsForUpdateParams{Ids: ids, TenantID: tenantID})
	if err != nil {
		writeTxError(w, err, "failed to lock products")
		return
	}
	products := make(map[[16]byte]dbgen.AdminGetProductsByIDsForUpdateRow, len(rows))
	for _, row := range rows {
		products[row.ID.Bytes] = row
	}
	for _, t := range targets {
		if _, found := products[t.productID.Bytes]; !found {
			errs.add(t.index, "productId", codeNotFound, "product not found")
		}
	}
	if len(errs) > 0 {
		writeBulkValidation(w, errs)
		return
	}
	var changes []catalog.ProductChange
	auditItems := make([]bulkAuditItem, 0, len(targets))
	results := make([]map[string]any, 0, len(targets))
	updated := 0
	for _, t := range targets {
		p := products[t.productID.Bytes]
		if p.Status != t.status {
			if _, err := q.AdminBulkSetProductStatus(ctx, dbgen.AdminBulkSetProductStatusParams{Status: t.status, ID: p.ID, TenantID: tenantID}); err != nil {
				writeTxError(w, err, "failed to update status")
				return
			}
			updated++
			change := catalog.ProductChange{ProductID: p.ID, Slug: p.Slug, Field: catalog.FieldStatus, Old: p.Status, New: t.status}
			changes = append(changes, change)
			auditItems = append(auditItems, bulkAuditItem{ProductID: uuidString(p.ID), Slug: p.Slug, Changes: changeMap([]catalog.ProductChange{change})})
		}
		results = append(results, map[string]any{"productId": uuidString(p.ID), "slug": p.Slug, "status": t.status})
	}
	h.commitBulk(w, r, tx, q, "catalog.bulk.status", len(targets), updated, auditItems, changes, results)
}
