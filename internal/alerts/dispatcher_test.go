package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/money"
)

func TestSelectHitsBudget(t *testing.T) {
	hits := make([]hit, 5)
	got, more := selectHits(hits, 2)
	if len(got) != 2 || !more {
		t.Fatalf("len=%d more=%v, want 2 true", len(got), more)
	}
	got, more = selectHits(hits[:2], 2)
	if len(got) != 2 || more {
		t.Fatalf("len=%d more=%v, want 2 false", len(got), more)
	}
	got, more = selectHits(nil, 2)
	if len(got) != 0 || more {
		t.Fatalf("empty: len=%d more=%v", len(got), more)
	}
}

func TestBuildMessagePriceDrop(t *testing.T) {
	h := hit{
		SubscriptionID: newID(), TenantID: newID(), UserID: newID(), ProductID: newID(),
		Email: "buyer@example.com", ProductTitle: "Kaos Hitam", ProductSlug: "kaos-hitam",
		Price: 999000, PreviousPrice: 1250000,
	}
	m, err := buildMessage(TypePriceDrop, h, "IDR")
	if err != nil {
		t.Fatal(err)
	}
	want := "Kaos Hitam turun dari " + money.Format("IDR", 1250000) + " menjadi " + money.Format("IDR", 999000) + "."
	if m.Body != want || m.Body != "Kaos Hitam turun dari Rp 1.250.000 menjadi Rp 999.000." {
		t.Fatalf("body = %q", m.Body)
	}
	if m.Title != "Harga turun" || m.Kind != KindPriceDrop || m.Topic != events.TopicAlertPriceDrop {
		t.Fatalf("message = %+v", m)
	}
	var data, payload map[string]any
	_ = json.Unmarshal(m.Data, &data)
	_ = json.Unmarshal(m.Payload, &payload)
	if data["previousPrice"].(float64) != 1250000 || data["price"].(float64) != 999000 || data["variantId"] != nil || data["topic"] != events.TopicAlertPriceDrop {
		t.Fatalf("data = %v", data)
	}
	if payload["email"] != "buyer@example.com" || payload["message"] != want || payload["previousPrice"].(float64) != 1250000 {
		t.Fatalf("payload = %v", payload)
	}
}

func TestBuildMessageBackInStock(t *testing.T) {
	h := hit{SubscriptionID: newID(), TenantID: newID(), UserID: newID(), ProductID: newID(), VariantID: newID(), ProductTitle: "Kaos", Price: 249000}
	m, err := buildMessage(TypeBackInStock, h, "IDR")
	if err != nil {
		t.Fatal(err)
	}
	if m.Body != "Kaos sudah tersedia kembali." || m.Title != "Stok kembali tersedia" || m.Kind != KindBackInStock {
		t.Fatalf("message = %+v", m)
	}
	var data map[string]any
	_ = json.Unmarshal(m.Data, &data)
	if _, has := data["previousPrice"]; has {
		t.Fatal("back_in_stock must not carry previousPrice")
	}
	if data["variantId"] == nil {
		t.Fatal("variantId should be set")
	}
	if _, err := buildMessage("NOPE", h, "IDR"); err == nil {
		t.Fatal("unknown type must error")
	}
}

type prefsStub struct {
	p   dbgen.PushPreference
	err error
}

func (s prefsStub) GetPushPreferences(context.Context, pgtype.UUID) (dbgen.PushPreference, error) {
	return s.p, s.err
}

func TestWantsStockUpdates(t *testing.T) {
	cases := []struct {
		name    string
		stub    prefsStub
		want    bool
		wantErr bool
	}{
		{"no row means opted in", prefsStub{err: pgx.ErrNoRows}, true, false},
		{"all on", prefsStub{p: dbgen.PushPreference{Enabled: true, StockUpdates: true}}, true, false},
		{"stock updates off", prefsStub{p: dbgen.PushPreference{Enabled: true, StockUpdates: false}}, false, false},
		{"master off", prefsStub{p: dbgen.PushPreference{Enabled: false, StockUpdates: true}}, false, false},
		{"db error", prefsStub{err: errors.New("boom")}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wantsStockUpdates(context.Background(), tc.stub, newID())
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got=%v err=%v", got, err)
			}
		})
	}
}

func TestPerEventShare(t *testing.T) {
	cases := []struct{ budget, events, want int }{
		{200, 1, 200},
		{200, 2, 100},
		{2, 2, 1},
		{2, 5, 1}, // rounds up to at least one claim per event
		{5, 2, 3}, // ceil
		{200, 0, 200},
	}
	for _, tc := range cases {
		if got := perEventShare(tc.budget, tc.events); got != tc.want {
			t.Errorf("perEventShare(%d,%d) = %d, want %d", tc.budget, tc.events, got, tc.want)
		}
	}
}

func TestFlushNilSafe(t *testing.T) {
	var d *Dispatcher
	if res, err := d.Flush(context.Background()); err != nil || res != (Result{}) {
		t.Fatalf("nil dispatcher: %+v %v", res, err)
	}
	if res, err := (&Dispatcher{}).Flush(context.Background()); err != nil || res != (Result{}) {
		t.Fatalf("nil pool: %+v %v", res, err)
	}
}
