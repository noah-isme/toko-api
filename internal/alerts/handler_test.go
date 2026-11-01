package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type fakeAPI struct {
	sub       Subscription
	created   bool
	err       error
	items     []Subscription
	total     int64
	deleted   bool
	gotInput  SubscribeInput
	gotFilter pgtype.UUID
}

func (f *fakeAPI) Subscribe(_ context.Context, _, _ pgtype.UUID, in SubscribeInput) (Subscription, bool, error) {
	f.gotInput = in
	return f.sub, f.created, f.err
}

func (f *fakeAPI) List(_ context.Context, _, _, productID pgtype.UUID, _, _ int32) ([]Subscription, int64, error) {
	f.gotFilter = productID
	return f.items, f.total, nil
}

func (f *fakeAPI) Unsubscribe(_ context.Context, _, _, _ pgtype.UUID) (bool, error) {
	return f.deleted, nil
}

func router(api API) http.Handler {
	h := &Handler{Svc: api}
	r := chi.NewRouter()
	r.Get("/alerts", h.List)
	r.Post("/alerts", h.Subscribe)
	r.Delete("/alerts/{id}", h.Unsubscribe)
	return r
}

func authed(req *http.Request) *http.Request {
	ctx := common.WithUserID(req.Context(), uuid.NewString())
	ctx = tenant.WithTenant(ctx, uuid.NewString())
	return req.WithContext(ctx)
}

func TestHandlerUnauthenticated(t *testing.T) {
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/alerts", nil),
		httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodDelete, "/alerts/"+uuid.NewString(), nil),
	} {
		rec := httptest.NewRecorder()
		router(&fakeAPI{}).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", req.Method, req.URL.Path, rec.Code)
		}
	}
}

func TestHandlerSubscribeStatusCodes(t *testing.T) {
	pid := uuid.NewString()
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	base := Subscription{
		ID: newID(), ProductID: newID(), Type: TypePriceDrop,
		PriceAtSubscribe: pgtype.Int8{Int64: 150000, Valid: true},
		CreatedAt:        ts(now),
		Product:          ProductSummary{Title: "Kaos", Slug: "kaos", Price: 150000, InStock: true},
	}
	cases := []struct {
		name string
		body string
		api  *fakeAPI
		want int
		code string
	}{
		{"created", `{"productId":"` + pid + `","type":"PRICE_DROP"}`, &fakeAPI{sub: base, created: true}, 201, ""},
		{"re-armed", `{"productId":"` + pid + `","type":"price_drop"}`, &fakeAPI{sub: base}, 200, ""},
		{"bad product id", `{"productId":"nope","type":"PRICE_DROP"}`, &fakeAPI{}, 400, "BAD_REQUEST"},
		{"bad variant id", `{"productId":"` + pid + `","variantId":"x","type":"PRICE_DROP"}`, &fakeAPI{}, 400, "BAD_REQUEST"},
		{"bad type", `{"productId":"` + pid + `","type":"WHATEVER"}`, &fakeAPI{}, 400, "BAD_REQUEST"},
		{"bad json", `{`, &fakeAPI{}, 400, "BAD_REQUEST"},
		{"not found", `{"productId":"` + pid + `","type":"BACK_IN_STOCK"}`, &fakeAPI{err: ErrNotFound}, 404, "NOT_FOUND"},
		{"variant mismatch", `{"productId":"` + pid + `","variantId":"` + uuid.NewString() + `","type":"BACK_IN_STOCK"}`, &fakeAPI{err: ErrVariantMismatch}, 404, "NOT_FOUND"},
		{"already in stock", `{"productId":"` + pid + `","type":"BACK_IN_STOCK"}`, &fakeAPI{err: ErrAlreadyInStock}, 409, "ALREADY_IN_STOCK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := authed(httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(tc.body)))
			router(tc.api).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.code != "" {
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				if body.Error.Code != tc.code {
					t.Fatalf("code = %q, want %q", body.Error.Code, tc.code)
				}
				return
			}
			var body struct {
				Data map[string]any `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data["status"] != "active" || body.Data["priceAtSubscribe"].(float64) != 150000 {
				t.Fatalf("unexpected body %v", body.Data)
			}
			if body.Data["variantId"] != nil {
				t.Fatalf("variantId should be null, got %v", body.Data["variantId"])
			}
			if _, has := body.Data["variant"]; has {
				t.Fatal("variant must be omitted for product-level subscription")
			}
			if tc.api.gotInput.Type != TypePriceDrop {
				t.Fatalf("type not normalized: %q", tc.api.gotInput.Type)
			}
		})
	}
}

func TestHandlerListPaginationAndDTO(t *testing.T) {
	notified := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	api := &fakeAPI{total: 7, items: []Subscription{{
		ID: newID(), ProductID: newID(), VariantID: newID(), Type: TypeBackInStock,
		NotifiedAt: ts(notified), CreatedAt: ts(notified.Add(-time.Hour)),
		Product: ProductSummary{Title: "Kaos", Slug: "kaos", Thumbnail: pgtype.Text{String: "/k.jpg", Valid: true}, Price: 249000},
		Variant: &VariantSummary{ID: newID(), SKU: pgtype.Text{String: "K-1", Valid: true}, Price: 249000, Stock: 0},
	}}}
	pid := uuid.NewString()
	rec := httptest.NewRecorder()
	router(api).ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/alerts?page=2&limit=5&productId="+pid, nil)))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Data       []map[string]any `json:"data"`
		Pagination struct {
			Page, PerPage, TotalItems int
		} `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Pagination.Page != 2 || body.Pagination.PerPage != 5 || body.Pagination.TotalItems != 7 {
		t.Fatalf("pagination = %+v", body.Pagination)
	}
	if uuid.UUID(api.gotFilter.Bytes).String() != pid {
		t.Fatal("productId filter not forwarded")
	}
	item := body.Data[0]
	if item["status"] != "notified" || item["notifiedAt"] != "2026-10-02T09:00:00Z" || item["priceAtSubscribe"] != nil {
		t.Fatalf("item = %v", item)
	}
	if v, ok := item["variant"].(map[string]any); !ok || v["sku"] != "K-1" {
		t.Fatalf("variant = %v", item["variant"])
	}

	rec = httptest.NewRecorder()
	router(api).ServeHTTP(rec, authed(httptest.NewRequest(http.MethodGet, "/alerts?productId=bad", nil)))
	if rec.Code != 400 {
		t.Fatalf("bad productId status = %d", rec.Code)
	}
}

func TestHandlerUnsubscribe(t *testing.T) {
	rec := httptest.NewRecorder()
	router(&fakeAPI{deleted: true}).ServeHTTP(rec, authed(httptest.NewRequest(http.MethodDelete, "/alerts/"+uuid.NewString(), nil)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	rec = httptest.NewRecorder()
	router(&fakeAPI{deleted: false}).ServeHTTP(rec, authed(httptest.NewRequest(http.MethodDelete, "/alerts/"+uuid.NewString(), nil)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	router(&fakeAPI{}).ServeHTTP(rec, authed(httptest.NewRequest(http.MethodDelete, "/alerts/not-a-uuid", nil)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
