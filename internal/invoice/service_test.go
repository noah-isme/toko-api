package invoice

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

func TestEligible(t *testing.T) {
	cases := map[dbgen.OrderStatus]bool{
		dbgen.OrderStatusPENDINGPAYMENT: false,
		dbgen.OrderStatusPAID:           true,
		dbgen.OrderStatusPACKED:         true,
		dbgen.OrderStatusSHIPPED:        true,
		dbgen.OrderStatusOUTFORDELIVERY: true,
		dbgen.OrderStatusDELIVERED:      true,
		dbgen.OrderStatusCANCELLED:      false,
	}
	for status, want := range cases {
		if got := Eligible(status); got != want {
			t.Errorf("Eligible(%s) = %v, want %v", status, got, want)
		}
	}
}

func TestInvoiceNumber(t *testing.T) {
	if got := invoiceNumber(2026, 1); got != "INV-2026-000001" {
		t.Fatalf("got %q", got)
	}
	if got := invoiceNumber(2027, 1234567); got != "INV-2027-1234567" {
		t.Fatalf("got %q", got)
	}
}

func TestSettingsMapAndBuildDocument(t *testing.T) {
	settings := settingsMap([]dbgen.ListTenantSettingsByKeysRow{
		{Key: "storeName", Value: []byte(`"Toko Kaos"`)},
		{Key: "contactPhone", Value: []byte(`"+62 812"`)},
		{Key: "description", Value: []byte(`{"not":"a string"}`)},
		{Key: "contactEmail", Value: []byte(`"  "`)},
	})
	if settings["storeName"] != "Toko Kaos" || settings["contactPhone"] != "+62 812" {
		t.Fatalf("settings = %v", settings)
	}
	if _, has := settings["description"]; has {
		t.Fatal("non-string values must be ignored")
	}
	row := dbgen.GetOrderForInvoiceRow{
		OrderNumber:        pgtype.Text{String: "ORD-1", Valid: true},
		Status:             dbgen.OrderStatusPAID,
		Currency:           "IDR",
		PricingSubtotal:    1245000,
		PricingShipping:    5000,
		PricingTotal:       1250000,
		TenantName:         "Tenant Name",
		TenantContactEmail: pgtype.Text{String: "tenant@example.com", Valid: true},
		ShippingAddress:    []byte(`{"receiverName":"Budi","city":"Bandung","addressLine1":"Jl. A"}`),
		CustomerName:       pgtype.Text{String: "Budi", Valid: true},
	}
	doc := buildDocument(row, []dbgen.ListInvoiceItemsRow{{Title: "Kaos", Qty: 5, UnitPrice: 249000, Subtotal: 1245000, Sku: pgtype.Text{String: "K1", Valid: true}}}, settings)
	if doc.Store.Name != "Toko Kaos" || doc.Store.Email != "tenant@example.com" || doc.Store.Phone != "+62 812" {
		t.Fatalf("store = %+v", doc.Store)
	}
	if doc.Total != 1250000 || doc.Subtotal != 1245000 || doc.Items[0].UnitPrice != 249000 {
		t.Fatal("amounts must be copied verbatim")
	}
	if doc.Address.City != "Bandung" || doc.Address.Line1 != "Jl. A" || doc.Items[0].SKU != "K1" {
		t.Fatalf("doc = %+v", doc)
	}
	doc = buildDocument(row, nil, map[string]string{})
	if doc.Store.Name != "Tenant Name" {
		t.Fatalf("fallback store name = %q", doc.Store.Name)
	}
}

func TestLoadLocationFallback(t *testing.T) {
	if loadLocation("Not/AZone").String() != "UTC" || loadLocation("").String() != "UTC" {
		t.Fatal("invalid timezone must fall back to UTC")
	}
	if loadLocation("Asia/Jakarta").String() != "Asia/Jakarta" {
		t.Fatal("valid timezone not loaded")
	}
}
