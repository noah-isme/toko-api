package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Laptop Gaming ROG":  "laptop-gaming-rog",
		"  Multi   Space  ":  "multi-space",
		"Sạc & Cáp / USB-C":  "s-c-c-p-usb-c",
		"already-a-slug":     "already-a-slug",
		"!!!":                "",
		"Product 123 v2.0":   "product-123-v2-0",
		"UPPER_snake_case":   "upper-snake-case",
		"trailing dashes---": "trailing-dashes",
	}
	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseListQueryClampsBounds(t *testing.T) {
	cases := []struct {
		query      string
		page       int
		limit      int
		offset     int
		commentary string
	}{
		{"", 1, defaultLimit, 0, "defaults"},
		{"?page=3&limit=10", 3, 10, 20, "explicit values"},
		{"?page=0", 1, defaultLimit, 0, "page must be >= 1"},
		{"?page=-5", 1, defaultLimit, 0, "negative page ignored"},
		{"?limit=0", 1, defaultLimit, 0, "limit must be >= 1"},
		{"?limit=5000", 1, maxLimit, 0, "limit clamped to maxLimit"},
		{"?page=abc&limit=xyz", 1, defaultLimit, 0, "garbage ignored"},
		{"?page=2&limit=100", 2, 100, 100, "offset follows clamped limit"},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/admin/products"+tc.query, nil)
		page, limit, offset := parseListQuery(r)
		if page != tc.page || limit != tc.limit || offset != tc.offset {
			t.Errorf("%s: parseListQuery(%q) = (%d,%d,%d), want (%d,%d,%d)",
				tc.commentary, tc.query, page, limit, offset, tc.page, tc.limit, tc.offset)
		}
	}
}

func TestOrderStatusFilter(t *testing.T) {
	valid := map[string]dbgen.OrderStatus{
		"PAID":             dbgen.OrderStatusPAID,
		"paid":             dbgen.OrderStatusPAID,
		"pending_payment":  dbgen.OrderStatusPENDINGPAYMENT,
		"out_for_delivery": dbgen.OrderStatusOUTFORDELIVERY,
		"CANCELLED":        dbgen.OrderStatusCANCELLED,
	}
	for input, want := range valid {
		got, err := orderStatusFilter(input)
		if err != nil {
			t.Fatalf("orderStatusFilter(%q) unexpected error: %v", input, err)
		}
		if !got.Valid || got.OrderStatus != want {
			t.Errorf("orderStatusFilter(%q) = %+v, want %v", input, got, want)
		}
	}
	for _, input := range []string{"", "   ", "all", "ALL"} {
		got, err := orderStatusFilter(input)
		if err != nil {
			t.Fatalf("orderStatusFilter(%q) unexpected error: %v", input, err)
		}
		if got.Valid {
			t.Errorf("orderStatusFilter(%q) should be a no-op filter, got %+v", input, got)
		}
	}
	for _, input := range []string{"SHIPPING", "unknown", "DROP TABLE"} {
		if _, err := orderStatusFilter(input); err == nil {
			t.Errorf("orderStatusFilter(%q) expected error", input)
		}
	}
}

func TestDiscountKindFilterAcceptsUIAliases(t *testing.T) {
	valid := map[string]dbgen.DiscountKind{
		"percent":      dbgen.DiscountKindPercent,
		"percentage":   dbgen.DiscountKindPercent,
		"PERCENTAGE":   dbgen.DiscountKindPercent,
		"fixed":        dbgen.DiscountKindFixedAmount,
		"fixed_amount": dbgen.DiscountKindFixedAmount,
	}
	for input, want := range valid {
		got, err := discountKindFilter(input)
		if err != nil {
			t.Fatalf("discountKindFilter(%q) unexpected error: %v", input, err)
		}
		if !got.Valid || got.DiscountKind != want {
			t.Errorf("discountKindFilter(%q) = %+v, want %v", input, got, want)
		}
	}
	for _, input := range []string{"", "all"} {
		got, _ := discountKindFilter(input)
		if got.Valid {
			t.Errorf("discountKindFilter(%q) should be a no-op filter", input)
		}
	}
	if _, err := discountKindFilter("bogus"); err == nil {
		t.Error("discountKindFilter(\"bogus\") expected error")
	}
}

func TestRangeWindow(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		input string
		label string
		days  int // -1 means no start bound
	}{
		{"", "30d", 30},
		{"7d", "7d", 7},
		{"30d", "30d", 30},
		{"90d", "90d", 90},
		{"all", "all", -1},
		{"garbage", "30d", 30},
		{"0d", "30d", 30},
		{"-5d", "30d", 30},
	}
	for _, tc := range cases {
		label, start := rangeWindow(tc.input)
		if label != tc.label {
			t.Errorf("rangeWindow(%q) label = %q, want %q", tc.input, label, tc.label)
		}
		if tc.days < 0 {
			if start.Valid {
				t.Errorf("rangeWindow(%q) should have no start bound", tc.input)
			}
			continue
		}
		if !start.Valid {
			t.Fatalf("rangeWindow(%q) expected a start bound", tc.input)
		}
		want := now.AddDate(0, 0, -tc.days)
		if delta := start.Time.Sub(want); delta > time.Minute || delta < -time.Minute {
			t.Errorf("rangeWindow(%q) start = %v, want ~%v", tc.input, start.Time, want)
		}
	}
}

func TestQueryTimeParsesBothLayouts(t *testing.T) {
	r := httptest.NewRequest("GET", "/admin/orders?startDate=2026-01-15&endDate=2026-02-01T10:30:00Z&bad=nope", nil)
	start, err := queryTime(r, "startDate")
	if err != nil || !start.Valid || start.Time.Format("2006-01-02") != "2026-01-15" {
		t.Fatalf("startDate: got %v err %v", start, err)
	}
	end, err := queryTime(r, "endDate")
	if err != nil || !end.Valid || end.Time.UTC().Hour() != 10 {
		t.Fatalf("endDate: got %v err %v", end, err)
	}
	if missing, err := queryTime(r, "absent"); err != nil || missing.Valid {
		t.Fatalf("absent key should be a no-op, got %v err %v", missing, err)
	}
	if _, err := queryTime(r, "bad"); err == nil {
		t.Error("unparseable date should error")
	}
}

func TestQueryBoolOnlyValidWhenParseable(t *testing.T) {
	r := httptest.NewRequest("GET", "/admin/products?a=true&b=false&c=1&d=maybe", nil)
	for key, want := range map[string]bool{"a": true, "b": false, "c": true} {
		got := queryBool(r, key)
		if !got.Valid || got.Bool != want {
			t.Errorf("queryBool(%q) = %+v, want %v", key, got, want)
		}
	}
	for _, key := range []string{"d", "absent"} {
		if got := queryBool(r, key); got.Valid {
			t.Errorf("queryBool(%q) should be invalid, got %+v", key, got)
		}
	}
}

func TestDecodeProductPayloadTracksPresentFields(t *testing.T) {
	r := httptest.NewRequest("PATCH", "/admin/products/x", stringReader(`{"title":"New","compareAt":null,"badges":[]}`))
	payload, err := decodeProductPayload(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !payload.has("title") || !payload.has("compareAt") || !payload.has("badges") {
		t.Errorf("expected title/compareAt/badges present, got %v", payload.fieldsPresent)
	}
	if payload.has("price") || payload.has("slug") {
		t.Error("absent keys must not be reported present")
	}
	if payload.Title == nil || *payload.Title != "New" {
		t.Errorf("title = %v", payload.Title)
	}
	// An explicit null must be distinguishable from an omitted key so PATCH can
	// clear compare_at rather than silently keeping the old value.
	if payload.CompareAt != nil {
		t.Errorf("compareAt should decode to nil, got %v", payload.CompareAt)
	}
}

func TestIsUniqueAndForeignKeyViolation(t *testing.T) {
	if !isUniqueViolation(errString("ERROR: duplicate key value (SQLSTATE 23505)")) {
		t.Error("expected unique violation detection")
	}
	if !isForeignKeyViolation(errString("ERROR: violates foreign key constraint (SQLSTATE 23503)")) {
		t.Error("expected FK violation detection")
	}
	if isUniqueViolation(nil) || isForeignKeyViolation(nil) {
		t.Error("nil error is not a violation")
	}
	if isUniqueViolation(errString("connection refused")) {
		t.Error("unrelated error misclassified")
	}
}

func stringReader(body string) *strings.Reader { return strings.NewReader(body) }

type errString string

func (e errString) Error() string { return string(e) }
