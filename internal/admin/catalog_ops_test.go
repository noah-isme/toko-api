package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/catalog"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

func TestParseProductSort(t *testing.T) {
	ok := map[string]string{
		"":             "created:desc",
		"name":         "name:asc",
		"price":        "price:asc",
		"stock":        "stock:asc",
		"created":      "created:desc",
		"updated":      "updated:desc",
		"name:desc":    "name:desc",
		"price:desc":   "price:desc",
		"stock:desc":   "stock:desc",
		"created:asc":  "created:asc",
		"updated:asc":  "updated:asc",
		" NAME:ASC ":   "name:asc",
		"updated:desc": "updated:desc",
	}
	for in, want := range ok {
		got, err := parseProductSort(in)
		if err != nil || got != want {
			t.Errorf("parseProductSort(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"title", "price:up", "name:", "created_at:desc", ":asc"} {
		if _, err := parseProductSort(in); err == nil {
			t.Errorf("parseProductSort(%q) should fail", in)
		}
	}
}

func TestParseStockStatus(t *testing.T) {
	for _, in := range []string{"in", "low", "out", " LOW "} {
		got, err := parseStockStatus(in)
		if err != nil || !got.Valid {
			t.Errorf("parseStockStatus(%q) = %v, %v", in, got, err)
		}
	}
	if got, err := parseStockStatus(""); err != nil || got.Valid {
		t.Errorf("empty stockStatus must be no filter, got %v %v", got, err)
	}
	for _, in := range []string{"instock", "none", "1"} {
		if _, err := parseStockStatus(in); err == nil {
			t.Errorf("parseStockStatus(%q) should fail", in)
		}
	}
}

func TestParseProductStatus(t *testing.T) {
	for _, in := range []string{"draft", "published", "archived", "Draft"} {
		got, err := parseProductStatus(in)
		if err != nil || !got.Valid {
			t.Errorf("parseProductStatus(%q) = %v, %v", in, got, err)
		}
	}
	if got, err := parseProductStatus(""); err != nil || got.Valid {
		t.Errorf("empty status must be no filter")
	}
	for _, in := range []string{"active", "deleted", "pub"} {
		if _, err := parseProductStatus(in); err == nil {
			t.Errorf("parseProductStatus(%q) should fail", in)
		}
	}
}

// ---------------------------------------------------------------------------
// Bulk validation (Pool == nil, so valid requests stop at 500 after validation)

type bulkErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			Rows []bulkRowError `json:"rows"`
		} `json:"details"`
	} `json:"error"`
}

func runBulk(t *testing.T, handler http.HandlerFunc, body string) (int, bulkErrorBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	var decoded bulkErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec.Code, decoded
}

func rowCodes(rows []bulkRowError) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Field+":"+r.Code)
	}
	return out
}

func TestBulkPriceValidation(t *testing.T) {
	h := &CatalogHandler{}
	p1, p2, v1 := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cases := []struct {
		name   string
		body   string
		status int
		code   string
		rows   []string
	}{
		{"malformed", `{"items":`, 400, "BAD_REQUEST", nil},
		{"unknown field", `{"items":[{"productId":"` + p1 + `","price":1,"foo":1}]}`, 400, "BAD_REQUEST", nil},
		{"empty", `{"items":[]}`, 400, "BAD_REQUEST", nil},
		{"both ids", `{"items":[{"productId":"` + p1 + `","variantId":"` + v1 + `","price":1}]}`, 422, "VALIDATION_FAILED", []string{"productId:AMBIGUOUS"}},
		{"neither id", `{"items":[{"price":1}]}`, 422, "VALIDATION_FAILED", []string{"productId:MISSING"}},
		{"bad uuid", `{"items":[{"productId":"nope","price":1}]}`, 422, "VALIDATION_FAILED", []string{"productId:INVALID_UUID"}},
		{"missing price", `{"items":[{"productId":"` + p1 + `"}]}`, 422, "VALIDATION_FAILED", []string{"price:MISSING"}},
		{"negative price", `{"items":[{"productId":"` + p1 + `","price":-1}]}`, 422, "VALIDATION_FAILED", []string{"price:NEGATIVE"}},
		{"negative compareAt", `{"items":[{"productId":"` + p1 + `","price":1,"compareAt":-5}]}`, 422, "VALIDATION_FAILED", []string{"compareAt:NEGATIVE"}},
		{"bad applyTo", `{"items":[{"productId":"` + p1 + `","price":1,"applyTo":"all"}]}`, 422, "VALIDATION_FAILED", []string{"applyTo:INVALID_APPLY_TO"}},
		{"applyTo on variant", `{"items":[{"variantId":"` + v1 + `","price":1,"applyTo":"both"}]}`, 422, "VALIDATION_FAILED", []string{"applyTo:NOT_ALLOWED"}},
		{"duplicate product", `{"items":[{"productId":"` + p1 + `","price":1},{"productId":"` + p1 + `","price":2}]}`, 422, "VALIDATION_FAILED", []string{"productId:DUPLICATE"}},
		{"valid reaches tx", `{"items":[{"productId":"` + p1 + `","price":1,"compareAt":null},{"productId":"` + p2 + `","price":2,"applyTo":"product"},{"variantId":"` + v1 + `","price":3}]}`, 500, "INTERNAL", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := runBulk(t, h.BulkPrice, tc.body)
			if status != tc.status || body.Error.Code != tc.code {
				t.Fatalf("status=%d code=%s, want %d %s", status, body.Error.Code, tc.status, tc.code)
			}
			if got := rowCodes(body.Error.Details.Rows); strings.Join(got, ",") != strings.Join(tc.rows, ",") {
				t.Fatalf("rows=%v want %v", got, tc.rows)
			}
		})
	}
}

func TestBulkRowLimitAndBodySize(t *testing.T) {
	h := &CatalogHandler{}
	items := make([]string, bulkMaxRows+1)
	for i := range items {
		items[i] = `{"productId":"` + uuid.NewString() + `","status":"draft"}`
	}
	status, body := runBulk(t, h.BulkStatus, `{"items":[`+strings.Join(items, ",")+`]}`)
	if status != 400 || body.Error.Code != "TOO_MANY_ROWS" {
		t.Fatalf("got %d %s", status, body.Error.Code)
	}
	huge := `{"items":[{"productId":"` + strings.Repeat("a", bulkMaxBytes) + `"}]}`
	if status, body := runBulk(t, h.BulkStatus, huge); status != 413 || body.Error.Code != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("got %d %s", status, body.Error.Code)
	}
}

func TestBulkStockValidation(t *testing.T) {
	h := &CatalogHandler{}
	v1 := uuid.NewString()
	cases := []struct {
		name   string
		body   string
		status int
		rows   []string
	}{
		{"missing variant", `{"items":[{"stock":1}]}`, 422, []string{"variantId:MISSING"}},
		{"bad uuid", `{"items":[{"variantId":"x","stock":1}]}`, 422, []string{"variantId:INVALID_UUID"}},
		{"both", `{"items":[{"variantId":"` + v1 + `","stock":1,"delta":2}]}`, 422, []string{"stock:AMBIGUOUS"}},
		{"neither", `{"items":[{"variantId":"` + v1 + `"}]}`, 422, []string{"stock:MISSING"}},
		{"negative stock", `{"items":[{"variantId":"` + v1 + `","stock":-1}]}`, 422, []string{"stock:NEGATIVE"}},
		{"duplicate", `{"items":[{"variantId":"` + v1 + `","stock":1},{"variantId":"` + v1 + `","delta":-1}]}`, 422, []string{"variantId:DUPLICATE"}},
		{"productId rejected", `{"items":[{"productId":"` + v1 + `","stock":1}]}`, 400, nil},
		{"negative delta is fine", `{"items":[{"variantId":"` + v1 + `","delta":-3}]}`, 500, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := runBulk(t, h.BulkStock, tc.body)
			if status != tc.status {
				t.Fatalf("status=%d want %d", status, tc.status)
			}
			if got := rowCodes(body.Error.Details.Rows); strings.Join(got, ",") != strings.Join(tc.rows, ",") {
				t.Fatalf("rows=%v want %v", got, tc.rows)
			}
		})
	}
}

func TestBulkStatusValidation(t *testing.T) {
	h := &CatalogHandler{}
	p1 := uuid.NewString()
	cases := []struct {
		name   string
		body   string
		status int
		rows   []string
	}{
		{"missing status", `{"items":[{"productId":"` + p1 + `"}]}`, 422, []string{"status:MISSING"}},
		{"invalid status", `{"items":[{"productId":"` + p1 + `","status":"hidden"}]}`, 422, []string{"status:INVALID_STATUS"}},
		{"missing product", `{"items":[{"status":"draft"}]}`, 422, []string{"productId:MISSING"}},
		{"duplicate", `{"items":[{"productId":"` + p1 + `","status":"draft"},{"productId":"` + p1 + `","status":"archived"}]}`, 422, []string{"productId:DUPLICATE"}},
		{"multiple errors sorted", `{"items":[{"productId":"bad","status":"x"},{"status":"draft"}]}`, 422, []string{"productId:INVALID_UUID", "status:INVALID_STATUS", "productId:MISSING"}},
		{"valid", `{"items":[{"productId":"` + p1 + `","status":"ARCHIVED"}]}`, 500, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := runBulk(t, h.BulkStatus, tc.body)
			if status != tc.status {
				t.Fatalf("status=%d want %d", status, tc.status)
			}
			if got := rowCodes(body.Error.Details.Rows); strings.Join(got, ",") != strings.Join(tc.rows, ",") {
				t.Fatalf("rows=%v want %v", got, tc.rows)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Side effects

type recordingEventStore struct {
	params []dbgen.InsertDomainEventParams
}

func (s *recordingEventStore) InsertDomainEvent(_ context.Context, arg dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error) {
	s.params = append(s.params, arg)
	return dbgen.DomainEvent{ID: arg.AggregateID, Topic: arg.Topic, AggregateID: arg.AggregateID, Payload: arg.Payload, TenantID: arg.TenantID}, nil
}

func (s *recordingEventStore) MarkDomainEventDispatched(context.Context, pgtype.UUID) error {
	return nil
}

func (s *recordingEventStore) RecordDomainEventDispatchFailure(context.Context, dbgen.RecordDomainEventDispatchFailureParams) error {
	return nil
}

func pgID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func TestAfterProductChangesEmitsOncePerChange(t *testing.T) {
	store := &recordingEventStore{}
	h := &CatalogHandler{Events: &events.Bus{Store: store}}
	tenantID := uuid.NewString()
	ctx := tenant.WithTenant(context.Background(), tenantID)
	productID, variantID := pgID(), pgID()
	h.afterProductChanges(ctx, catalog.ChangeSourceBulk, []catalog.ProductChange{
		{ProductID: productID, Slug: "a", Field: catalog.FieldPrice, Old: int64(100), New: int64(120)},
		{ProductID: productID, VariantID: variantID, Slug: "a", SKU: "SKU-1", Field: catalog.FieldStock, Old: int32(4), New: int32(9)},
		{ProductID: productID, Slug: "a", Field: catalog.FieldStatus, Old: "published", New: "published"}, // unchanged
		{ProductID: productID, Slug: "a", Field: catalog.FieldStatus, Old: "published", New: "draft"},
	})
	if len(store.params) != 3 {
		t.Fatalf("emitted %d events, want 3", len(store.params))
	}
	wantTopics := []string{events.TopicProductPriceChanged, events.TopicProductStockChanged, events.TopicProductStatusChanged}
	for i, p := range store.params {
		if p.Topic != wantTopics[i] {
			t.Errorf("event %d topic %s want %s", i, p.Topic, wantTopics[i])
		}
		if !p.TenantID.Valid || uuid.UUID(p.TenantID.Bytes).String() != tenantID {
			t.Errorf("event %d not stamped with tenant", i)
		}
		var payload map[string]any
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["source"] != "bulk" || payload["tenantId"] != tenantID {
			t.Errorf("payload %v", payload)
		}
		for _, k := range []string{"email", "recipient", "customerEmail", "userEmail"} {
			if _, ok := payload[k]; ok {
				t.Errorf("payload must not carry %s", k)
			}
		}
	}
	var stock map[string]any
	_ = json.Unmarshal(store.params[1].Payload, &stock)
	if stock["variantId"] != uuid.UUID(variantID.Bytes).String() || stock["sku"] != "SKU-1" || stock["old"] != float64(4) || stock["new"] != float64(9) {
		t.Errorf("stock payload %v", stock)
	}
}

func TestAfterProductChangesSkipsWithoutTenant(t *testing.T) {
	store := &recordingEventStore{}
	h := &CatalogHandler{Events: &events.Bus{Store: store}}
	h.afterProductChanges(context.Background(), catalog.ChangeSourceSingle, []catalog.ProductChange{
		{ProductID: pgID(), Slug: "a", Field: catalog.FieldPrice, Old: int64(1), New: int64(2)},
	})
	if len(store.params) != 0 {
		t.Fatalf("emitted %d events without a tenant", len(store.params))
	}
}
