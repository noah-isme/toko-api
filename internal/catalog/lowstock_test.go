package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

func TestParseLowStockThreshold(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int32
	}{
		{"empty", ``, DefaultLowStockThreshold},
		{"malformed", `{lowStock`, DefaultLowStockThreshold},
		{"missing key", `{"other":1}`, DefaultLowStockThreshold},
		{"string", `{"lowStockThreshold":"7"}`, DefaultLowStockThreshold},
		{"float", `{"lowStockThreshold":2.5}`, DefaultLowStockThreshold},
		{"integral float", `{"lowStockThreshold":8.0}`, 8},
		{"negative", `{"lowStockThreshold":-1}`, DefaultLowStockThreshold},
		{"above max", `{"lowStockThreshold":1000001}`, DefaultLowStockThreshold},
		{"huge", `{"lowStockThreshold":1e30}`, DefaultLowStockThreshold},
		{"max", `{"lowStockThreshold":1000000}`, 1000000},
		{"zero", `{"lowStockThreshold":0}`, 0},
		{"valid", `{"lowStockThreshold":12}`, 12},
		{"null", `{"lowStockThreshold":null}`, DefaultLowStockThreshold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseLowStockThreshold([]byte(tc.raw)); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestStockStatus(t *testing.T) {
	cases := []struct {
		name                       string
		variants, stock, threshold int32
		flag                       bool
		want                       string
	}{
		{"no variants flag true", 0, 0, 5, true, StockStatusIn},
		{"no variants flag false", 0, 0, 5, false, StockStatusOut},
		{"zero stock", 2, 0, 5, true, StockStatusOut},
		{"below threshold", 1, 3, 5, true, StockStatusLow},
		{"equal threshold", 1, 5, 5, true, StockStatusLow},
		{"above threshold", 1, 6, 5, false, StockStatusIn},
		{"zero threshold never low", 1, 1, 0, true, StockStatusIn},
		{"zero threshold out", 1, 0, 0, true, StockStatusOut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StockStatus(tc.variants, tc.stock, tc.threshold, tc.flag); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestEffectiveThreshold(t *testing.T) {
	if got := EffectiveThreshold(pgtype.Int4{Int32: 2, Valid: true}, 9); got != 2 {
		t.Fatalf("override: got %d", got)
	}
	if got := EffectiveThreshold(pgtype.Int4{}, 9); got != 9 {
		t.Fatalf("default: got %d", got)
	}
}

type settingStub struct {
	raw []byte
	err error
	key string
}

func (s *settingStub) GetTenantSetting(_ context.Context, arg dbgen.GetTenantSettingParams) ([]byte, error) {
	s.key = arg.Key
	return s.raw, s.err
}

func TestResolveTenantLowStockThreshold(t *testing.T) {
	tenantID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	stub := &settingStub{err: pgx.ErrNoRows}
	got, err := ResolveTenantLowStockThreshold(context.Background(), stub, tenantID)
	if err != nil || got != DefaultLowStockThreshold {
		t.Fatalf("missing row: got %d, %v", got, err)
	}
	if stub.key != InventorySettingsKey {
		t.Fatalf("key = %q", stub.key)
	}
	stub = &settingStub{raw: []byte(`{"lowStockThreshold":20}`)}
	if got, _ := ResolveTenantLowStockThreshold(context.Background(), stub, tenantID); got != 20 {
		t.Fatalf("got %d", got)
	}
	stub = &settingStub{err: errors.New("db down")}
	if got, err := ResolveTenantLowStockThreshold(context.Background(), stub, tenantID); err == nil || got != DefaultLowStockThreshold {
		t.Fatalf("error case: got %d, %v", got, err)
	}
	if got, err := ResolveTenantLowStockThreshold(context.Background(), stub, pgtype.UUID{}); err != nil || got != DefaultLowStockThreshold {
		t.Fatalf("no tenant: got %d, %v", got, err)
	}
}
