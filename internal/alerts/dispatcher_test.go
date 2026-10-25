package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

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

func TestClaimBudget(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := &Dispatcher{EmitTimeout: 20 * time.Second, Now: func() time.Time { return now }}
	if got := d.claimBudget(context.Background()); got != defaultMaxSubscriptions {
		t.Fatalf("no deadline: %d", got)
	}
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(60*time.Second))
	defer cancel()
	if got := d.claimBudget(ctx); got != 3 {
		t.Fatalf("60s/20s: %d, want 3", got)
	}
	d.MaxSubscriptions = 2
	if got := d.claimBudget(ctx); got != 2 {
		t.Fatalf("max wins: %d, want 2", got)
	}
	short, cancel2 := context.WithDeadline(context.Background(), now.Add(19*time.Second))
	defer cancel2()
	if got := d.claimBudget(short); got != 0 {
		t.Fatalf("19s/20s: %d, want 0", got)
	}
	past, cancel3 := context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel3()
	if got := d.claimBudget(past); got != 0 {
		t.Fatalf("past deadline: %d, want 0", got)
	}
}

// slowDeliverer simulates a slow SMTP relay: each delivery takes `delay` and
// fails if its context is already done.
type slowDeliverer struct {
	delay time.Duration
	mu    sync.Mutex
	got   []pgtype.UUID
}

func (s *slowDeliverer) Deliver(ctx context.Context, ev dbgen.DomainEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	s.got = append(s.got, ev.ID)
	s.mu.Unlock()
	return nil
}

func TestDeliverIgnoresRunDeadline(t *testing.T) {
	slow := &slowDeliverer{delay: 20 * time.Millisecond}
	var errs []error
	d := &Dispatcher{Events: slow, EmitTimeout: time.Second, OnError: func(err error) { errs = append(errs, err) }}

	// The run deadline expires after ~2 deliveries; all 6 queued events must
	// still be delivered.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Millisecond)
	defer cancel()
	queued := make([]dbgen.DomainEvent, 6)
	for i := range queued {
		queued[i] = dbgen.DomainEvent{ID: newID(), Topic: events.TopicAlertBackInStock, AggregateID: newID()}
	}
	delivered := d.deliver(ctx, queued)
	if delivered != len(queued) || len(slow.got) != len(queued) {
		t.Fatalf("delivered=%d got=%v errs=%v", delivered, slow.got, errs)
	}
	if ctx.Err() == nil {
		t.Fatal("test precondition: run deadline should have expired during delivery")
	}

	// Even a context that is already cancelled must not drop payloads.
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	slow.got = nil
	if n := d.deliver(cancelled, queued[:2]); n != 2 {
		t.Fatalf("cancelled parent: delivered=%d, want 2 (errs=%v)", n, errs)
	}
}

func TestDeliverPerEmitTimeoutBoundsEachSend(t *testing.T) {
	slow := &slowDeliverer{delay: 200 * time.Millisecond}
	var errs []error
	d := &Dispatcher{Events: slow, EmitTimeout: 10 * time.Millisecond, OnError: func(err error) { errs = append(errs, err) }}
	n := d.deliver(context.Background(), []dbgen.DomainEvent{{ID: newID(), AggregateID: newID()}, {ID: newID(), AggregateID: newID()}})
	if n != 0 || len(errs) != 2 {
		t.Fatalf("delivered=%d errs=%d; a hung send must time out individually and be reported", n, len(errs))
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
