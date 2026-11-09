package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Low-stock classification shared by the admin product list, the inventory
// list, the products export and the admin overview. The SQL `stock_status`
// CASE in internal/db/queries/admin_catalog.sql mirrors StockStatus; keep them
// in sync.

const (
	// DefaultLowStockThreshold applies when neither the product nor the tenant
	// settings define a threshold.
	DefaultLowStockThreshold int32 = 5
	// MaxLowStockThreshold is the largest accepted threshold (matches the
	// products_low_stock_threshold_check constraint).
	MaxLowStockThreshold int32 = 1_000_000
	// InventorySettingsKey is the tenant_settings key that holds
	// {"lowStockThreshold": n}.
	InventorySettingsKey = "inventory"
)

// Stock status values.
const (
	StockStatusIn  = "in"
	StockStatusLow = "low"
	StockStatusOut = "out"
)

// ValidLowStockThreshold reports whether v is an accepted threshold value.
func ValidLowStockThreshold(v int64) bool {
	return v >= 0 && v <= int64(MaxLowStockThreshold)
}

// ParseLowStockThreshold reads lowStockThreshold from the tenant "inventory"
// settings document. Anything missing, malformed, fractional or out of range
// falls back to DefaultLowStockThreshold.
func ParseLowStockThreshold(raw []byte) int32 {
	if len(raw) == 0 {
		return DefaultLowStockThreshold
	}
	var doc struct {
		LowStockThreshold *float64 `json:"lowStockThreshold"`
	}
	// A JSON string ("7") fails to decode into *float64 and falls back.
	if err := json.Unmarshal(raw, &doc); err != nil || doc.LowStockThreshold == nil {
		return DefaultLowStockThreshold
	}
	f := *doc.LowStockThreshold
	if math.Trunc(f) != f || f < 0 || f > float64(MaxLowStockThreshold) {
		return DefaultLowStockThreshold
	}
	return int32(f)
}

// EffectiveThreshold returns the product override when set, else the tenant default.
func EffectiveThreshold(override pgtype.Int4, tenantDefault int32) int32 {
	if override.Valid {
		return override.Int32
	}
	return tenantDefault
}

// StockStatus classifies stock: a product without variants follows its
// in_stock flag; otherwise 0 is "out", 1..threshold is "low" and anything
// above is "in". A threshold of 0 therefore never reports "low".
func StockStatus(variantCount, totalStock, threshold int32, inStockFlag bool) string {
	if variantCount == 0 {
		if inStockFlag {
			return StockStatusIn
		}
		return StockStatusOut
	}
	if totalStock <= 0 {
		return StockStatusOut
	}
	if totalStock <= threshold {
		return StockStatusLow
	}
	return StockStatusIn
}

// ValidStockStatus reports whether s is one of in, low, out.
func ValidStockStatus(s string) bool {
	return s == StockStatusIn || s == StockStatusLow || s == StockStatusOut
}

// TenantSettingGetter is the query ResolveTenantLowStockThreshold needs.
type TenantSettingGetter interface {
	GetTenantSetting(ctx context.Context, arg dbgen.GetTenantSettingParams) ([]byte, error)
}

// ResolveTenantLowStockThreshold returns the tenant default threshold. A
// missing settings row yields DefaultLowStockThreshold; other errors are
// returned with the default so callers can choose to fail or degrade.
func ResolveTenantLowStockThreshold(ctx context.Context, q TenantSettingGetter, tenantID pgtype.UUID) (int32, error) {
	if q == nil || !tenantID.Valid {
		return DefaultLowStockThreshold, nil
	}
	raw, err := q.GetTenantSetting(ctx, dbgen.GetTenantSettingParams{TenantID: tenantID, Key: InventorySettingsKey})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DefaultLowStockThreshold, nil
		}
		return DefaultLowStockThreshold, err
	}
	return ParseLowStockThreshold(raw), nil
}
