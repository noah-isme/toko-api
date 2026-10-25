package alerts

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type stubStore struct {
	target    dbgen.GetAlertTargetRow
	targetErr error
	// recheckTarget, when set, is returned from the second GetAlertTarget
	// call (the post-commit recheck) instead of target.
	recheckTarget *dbgen.GetAlertTargetRow
	targetCalls   int
	enqueued      []dbgen.EnqueueAlertEventParams
	upsertArg     dbgen.UpsertAlertSubscriptionParams
	upsertRow     dbgen.ProductAlertSubscription
	upserted      bool
	listArg       dbgen.ListAlertSubscriptionsForUserParams
	listRows      []dbgen.ListAlertSubscriptionsForUserRow
	total         int64
	deleted       int64
	deleteArg     dbgen.DeleteAlertSubscriptionParams
}

func (s *stubStore) GetAlertTarget(_ context.Context, _ dbgen.GetAlertTargetParams) (dbgen.GetAlertTargetRow, error) {
	s.targetCalls++
	if s.targetCalls > 1 && s.recheckTarget != nil {
		return *s.recheckTarget, s.targetErr
	}
	return s.target, s.targetErr
}

func (s *stubStore) EnqueueAlertEvent(_ context.Context, arg dbgen.EnqueueAlertEventParams) error {
	s.enqueued = append(s.enqueued, arg)
	return nil
}

func (s *stubStore) UpsertAlertSubscription(_ context.Context, arg dbgen.UpsertAlertSubscriptionParams) (dbgen.ProductAlertSubscription, error) {
	s.upserted = true
	s.upsertArg = arg
	row := s.upsertRow
	row.ProductID = arg.ProductID
	row.VariantID = arg.VariantID
	row.AlertType = arg.AlertType
	row.PriceAtSubscribe = arg.PriceAtSubscribe
	return row, nil
}

func (s *stubStore) ListAlertSubscriptionsForUser(_ context.Context, arg dbgen.ListAlertSubscriptionsForUserParams) ([]dbgen.ListAlertSubscriptionsForUserRow, error) {
	s.listArg = arg
	return s.listRows, nil
}

func (s *stubStore) CountAlertSubscriptionsForUser(_ context.Context, _ dbgen.CountAlertSubscriptionsForUserParams) (int64, error) {
	return s.total, nil
}

func (s *stubStore) DeleteAlertSubscription(_ context.Context, arg dbgen.DeleteAlertSubscriptionParams) (int64, error) {
	s.deleteArg = arg
	return s.deleted, nil
}

func newID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func TestSellable(t *testing.T) {
	// Exactly the cart AddItem rule: variant lines look at variant stock only,
	// product-level lines at products.in_stock only.
	cases := []struct {
		name         string
		inStock      bool
		hasVariant   bool
		variantStock int32
		want         bool
	}{
		{"product flag off", false, false, 0, false},
		{"product flag on", true, false, 0, true},
		{"chosen variant zero", true, true, 0, false},
		{"chosen variant has stock", true, true, 3, true},
		{"chosen variant stock but flag off", false, true, 3, true},
		{"chosen variant negative", true, true, -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sellable(tc.inStock, tc.hasVariant, tc.variantStock); got != tc.want {
				t.Fatalf("Sellable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSubscribeBackInStockWhileSellableConflicts(t *testing.T) {
	st := &stubStore{target: dbgen.GetAlertTargetRow{ProductID: newID(), InStock: true, Price: 1000}}
	svc := &Service{Q: st}
	_, _, err := svc.Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypeBackInStock})
	if !errors.Is(err, ErrAlreadyInStock) {
		t.Fatalf("err = %v, want ErrAlreadyInStock", err)
	}
	if st.upserted {
		t.Fatal("must not upsert when already in stock")
	}
}

func TestSubscribeVariantOfOtherProductNotFound(t *testing.T) {
	// The LEFT JOIN on product_id yields no variant columns when the variant
	// belongs to another product.
	st := &stubStore{target: dbgen.GetAlertTargetRow{ProductID: newID(), InStock: false}}
	svc := &Service{Q: st}
	_, _, err := svc.Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), VariantID: newID(), Type: TypeBackInStock})
	if !errors.Is(err, ErrVariantMismatch) {
		t.Fatalf("err = %v, want ErrVariantMismatch", err)
	}
}

func TestSubscribeMissingProductNotFound(t *testing.T) {
	svc := &Service{Q: &stubStore{targetErr: pgx.ErrNoRows}}
	_, _, err := svc.Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypePriceDrop})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSubscribeInvalidType(t *testing.T) {
	svc := &Service{Q: &stubStore{}}
	_, _, err := svc.Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: "SOMETHING"})
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("err = %v, want ErrInvalidType", err)
	}
}

func TestSubscribePriceDropStoresEffectivePrice(t *testing.T) {
	now := time.Now()
	t.Run("product price", func(t *testing.T) {
		st := &stubStore{
			target:    dbgen.GetAlertTargetRow{ProductID: newID(), InStock: true, Price: 1250000},
			upsertRow: dbgen.ProductAlertSubscription{ID: newID(), CreatedAt: ts(now), UpdatedAt: ts(now)},
		}
		sub, created, err := (&Service{Q: st}).Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypePriceDrop})
		if err != nil {
			t.Fatal(err)
		}
		if !created {
			t.Fatal("expected created")
		}
		if !st.upsertArg.PriceAtSubscribe.Valid || st.upsertArg.PriceAtSubscribe.Int64 != 1250000 {
			t.Fatalf("price = %+v", st.upsertArg.PriceAtSubscribe)
		}
		if sub.Variant != nil {
			t.Fatal("product-level subscription must not carry a variant")
		}
	})
	t.Run("variant price", func(t *testing.T) {
		vid := newID()
		st := &stubStore{
			target: dbgen.GetAlertTargetRow{
				ProductID: newID(), InStock: true, Price: 1250000,
				VariantID: vid, VariantPrice: pgtype.Int8{Int64: 999000, Valid: true}, VariantStock: pgtype.Int4{Int32: 4, Valid: true},
			},
			upsertRow: dbgen.ProductAlertSubscription{ID: newID(), CreatedAt: ts(now), UpdatedAt: ts(now)},
		}
		sub, _, err := (&Service{Q: st}).Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), VariantID: vid, Type: TypePriceDrop})
		if err != nil {
			t.Fatal(err)
		}
		if st.upsertArg.PriceAtSubscribe.Int64 != 999000 {
			t.Fatalf("price = %d, want variant price", st.upsertArg.PriceAtSubscribe.Int64)
		}
		if sub.Variant == nil || sub.Variant.Price != 999000 || sub.Variant.Stock != 4 {
			t.Fatalf("variant = %+v", sub.Variant)
		}
	})
}

func TestSubscribeReArmReportsNotCreated(t *testing.T) {
	created := time.Now().Add(-time.Hour)
	st := &stubStore{
		target:    dbgen.GetAlertTargetRow{ProductID: newID(), InStock: false},
		upsertRow: dbgen.ProductAlertSubscription{ID: newID(), CreatedAt: ts(created), UpdatedAt: ts(time.Now())},
	}
	_, isNew, err := (&Service{Q: st}).Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypeBackInStock})
	if err != nil {
		t.Fatal(err)
	}
	if isNew {
		t.Fatal("re-arm must report created=false")
	}
	if st.upsertArg.PriceAtSubscribe.Valid {
		t.Fatal("BACK_IN_STOCK must not store a price")
	}
}

func TestListClampsPagination(t *testing.T) {
	st := &stubStore{total: 3, listRows: []dbgen.ListAlertSubscriptionsForUserRow{{ID: newID(), ProductID: newID(), AlertType: TypeBackInStock}}}
	items, total, err := (&Service{Q: st}).List(context.Background(), newID(), newID(), pgtype.UUID{}, 3, 500)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 1 {
		t.Fatalf("total=%d items=%d", total, len(items))
	}
	if st.listArg.RowLimit != 100 || st.listArg.RowOffset != 200 {
		t.Fatalf("limit=%d offset=%d", st.listArg.RowLimit, st.listArg.RowOffset)
	}
}

type kickCounter struct{ n int }

func (k *kickCounter) Kick() { k.n++ }

func TestSubscribeRecheckEnqueuesWhenRestockedMeanwhile(t *testing.T) {
	now := time.Now()
	vid := newID()
	sellable := dbgen.GetAlertTargetRow{ProductID: newID(), VariantID: vid, VariantStock: pgtype.Int4{Int32: 2, Valid: true}}
	st := &stubStore{
		target:        dbgen.GetAlertTargetRow{ProductID: newID(), VariantID: vid, VariantStock: pgtype.Int4{Int32: 0, Valid: true}},
		recheckTarget: &sellable,
		upsertRow:     dbgen.ProductAlertSubscription{ID: newID(), CreatedAt: ts(now), UpdatedAt: ts(now)},
	}
	kick := &kickCounter{}
	pid, tid := newID(), newID()
	if _, _, err := (&Service{Q: st, Alerts: kick}).Subscribe(context.Background(), newID(), tid, SubscribeInput{ProductID: pid, VariantID: vid, Type: TypeBackInStock}); err != nil {
		t.Fatal(err)
	}
	if len(st.enqueued) != 1 || kick.n != 1 {
		t.Fatalf("enqueued=%d kicks=%d, want 1/1", len(st.enqueued), kick.n)
	}
	got := st.enqueued[0]
	if got.TenantID != tid || got.ProductID != pid || got.VariantID != vid || got.AlertType != TypeBackInStock {
		t.Fatalf("enqueued = %+v", got)
	}
}

func TestSubscribeRecheckNoopWhenStillUnavailable(t *testing.T) {
	now := time.Now()
	st := &stubStore{
		target:    dbgen.GetAlertTargetRow{ProductID: newID(), InStock: false, Price: 1000},
		upsertRow: dbgen.ProductAlertSubscription{ID: newID(), CreatedAt: ts(now), UpdatedAt: ts(now)},
	}
	if _, _, err := (&Service{Q: st}).Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypeBackInStock}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&Service{Q: st}).Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypePriceDrop}); err != nil {
		t.Fatal(err)
	}
	if len(st.enqueued) != 0 {
		t.Fatalf("enqueued = %+v, want none", st.enqueued)
	}
}

func TestSubscribeRecheckPriceDroppedMeanwhile(t *testing.T) {
	now := time.Now()
	cheaper := dbgen.GetAlertTargetRow{ProductID: newID(), InStock: true, Price: 900}
	st := &stubStore{
		target:        dbgen.GetAlertTargetRow{ProductID: newID(), InStock: true, Price: 1000},
		recheckTarget: &cheaper,
		upsertRow:     dbgen.ProductAlertSubscription{ID: newID(), CreatedAt: ts(now), UpdatedAt: ts(now)},
	}
	if _, _, err := (&Service{Q: st}).Subscribe(context.Background(), newID(), newID(), SubscribeInput{ProductID: newID(), Type: TypePriceDrop}); err != nil {
		t.Fatal(err)
	}
	if len(st.enqueued) != 1 || st.enqueued[0].AlertType != TypePriceDrop {
		t.Fatalf("enqueued = %+v", st.enqueued)
	}
}

func TestListHugePageDoesNotOverflow(t *testing.T) {
	st := &stubStore{}
	if _, _, err := (&Service{Q: st}).List(context.Background(), newID(), newID(), pgtype.UUID{}, math.MaxInt32, 100); err != nil {
		t.Fatal(err)
	}
	if st.listArg.RowOffset < 0 {
		t.Fatalf("offset overflowed: %d", st.listArg.RowOffset)
	}
	if _, _, err := (&Service{Q: st}).List(context.Background(), newID(), newID(), pgtype.UUID{}, math.MaxInt32, 1); err != nil {
		t.Fatal(err)
	}
	if st.listArg.RowOffset < 0 {
		t.Fatalf("offset overflowed with limit 1: %d", st.listArg.RowOffset)
	}
}
