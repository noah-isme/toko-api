package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/catalog"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	productStatusDraft     = "draft"
	productStatusPublished = "published"
	productStatusArchived  = "archived"

	defaultProductSort = "created:desc"
)

// productListFilters are the filters shared by the admin product list, its
// count and the products export.
type productListFilters struct {
	Search      pgtype.Text
	Category    pgtype.Text
	Brand       pgtype.Text
	InStock     pgtype.Bool
	Status      pgtype.Text
	StockStatus pgtype.Text
}

// parseProductListFilters reads search, category, brand, inStock, status,
// stockStatus and sort. Unknown status/stockStatus/sort values are errors
// (400); the pre-existing inStock filter keeps its lenient parsing.
func parseProductListFilters(r *http.Request) (productListFilters, string, error) {
	status, err := parseProductStatus(r.URL.Query().Get("status"))
	if err != nil {
		return productListFilters{}, "", err
	}
	stockStatus, err := parseStockStatus(r.URL.Query().Get("stockStatus"))
	if err != nil {
		return productListFilters{}, "", err
	}
	sort, err := parseProductSort(r.URL.Query().Get("sort"))
	if err != nil {
		return productListFilters{}, "", err
	}
	return productListFilters{
		Search:      queryText(r, "search"),
		Category:    queryText(r, "category"),
		Brand:       queryText(r, "brand"),
		InStock:     queryBool(r, "inStock"),
		Status:      status,
		StockStatus: stockStatus,
	}, sort, nil
}

// parseProductSort canonicalises `field[:dir]` for the admin product list.
// Fields: name, price, stock, created, updated. A bare name/price/stock sorts
// ascending, a bare created/updated descending. Empty means created:desc.
func parseProductSort(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return defaultProductSort, nil
	}
	field, dir, hasDir := strings.Cut(value, ":")
	var defaultDir string
	switch field {
	case "name", "price", "stock":
		defaultDir = "asc"
	case "created", "updated":
		defaultDir = "desc"
	default:
		return "", fmt.Errorf("invalid sort field %q (use name, price, stock, created or updated)", field)
	}
	if !hasDir {
		dir = defaultDir
	}
	if dir != "asc" && dir != "desc" {
		return "", fmt.Errorf("invalid sort direction %q (use asc or desc)", dir)
	}
	return field + ":" + dir, nil
}

// parseStockStatus accepts in, low, out; empty means no filter.
func parseStockStatus(raw string) (pgtype.Text, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return pgtype.Text{}, nil
	}
	if !catalog.ValidStockStatus(value) {
		return pgtype.Text{}, errors.New("stockStatus must be in, low or out")
	}
	return pgtype.Text{String: value, Valid: true}, nil
}

// parseProductStatus accepts draft, published, archived; empty means no filter.
func parseProductStatus(raw string) (pgtype.Text, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "":
		return pgtype.Text{}, nil
	case productStatusDraft, productStatusPublished, productStatusArchived:
		return pgtype.Text{String: value, Valid: true}, nil
	}
	return pgtype.Text{}, errors.New("status must be draft, published or archived")
}

// payloadLowStockThreshold validates lowStockThreshold (0..1_000_000 or null).
func payloadLowStockThreshold(payload productPayload) (pgtype.Int4, error) {
	if payload.LowStockThreshold == nil {
		return pgtype.Int4{}, nil
	}
	if !catalog.ValidLowStockThreshold(*payload.LowStockThreshold) {
		return pgtype.Int4{}, fmt.Errorf("lowStockThreshold must be between 0 and %d", catalog.MaxLowStockThreshold)
	}
	return pgtype.Int4{Int32: int32(*payload.LowStockThreshold), Valid: true}, nil
}

// productUpdateChanges lists the event-worthy product-level changes a PATCH
// applied (price, compare_at, status).
func productUpdateChanges(existing dbgen.AdminGetProductRow, params dbgen.AdminUpdateProductParams, slug string) []catalog.ProductChange {
	var changes []catalog.ProductChange
	add := func(field string, oldValue, newValue any) {
		changes = append(changes, catalog.ProductChange{ProductID: existing.ID, Slug: slug, Field: field, Old: oldValue, New: newValue})
	}
	if params.Price.Valid && params.Price.Int64 != existing.Price {
		add(catalog.FieldPrice, existing.Price, params.Price.Int64)
	}
	if params.SetCompareAt && !int8Equal(params.CompareAt, existing.CompareAt) {
		add(catalog.FieldCompareAt, nullableInt64(existing.CompareAt), nullableInt64(params.CompareAt))
	}
	if params.Status.Valid && params.Status.String != existing.Status {
		add(catalog.FieldStatus, existing.Status, params.Status.String)
	}
	return changes
}

// variantChanges diffs variant price and stock before/after a product PATCH.
func variantChanges(productID pgtype.UUID, slug string, before, after []dbgen.ProductVariant) []catalog.ProductChange {
	old := make(map[[16]byte]dbgen.ProductVariant, len(before))
	for _, v := range before {
		old[v.ID.Bytes] = v
	}
	var changes []catalog.ProductChange
	for _, v := range after {
		prev, ok := old[v.ID.Bytes]
		if !ok {
			continue
		}
		if prev.Price != v.Price {
			changes = append(changes, catalog.ProductChange{ProductID: productID, VariantID: v.ID, Slug: slug, SKU: v.Sku.String, Field: catalog.FieldPrice, Old: prev.Price, New: v.Price})
		}
		if prev.Stock != v.Stock {
			changes = append(changes, catalog.ProductChange{ProductID: productID, VariantID: v.ID, Slug: slug, SKU: v.Sku.String, Field: catalog.FieldStock, Old: prev.Stock, New: v.Stock})
		}
	}
	return changes
}

func int8Equal(a, b pgtype.Int8) bool {
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || a.Int64 == b.Int64
}

func int4Equal(a, b pgtype.Int4) bool {
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || a.Int32 == b.Int32
}
