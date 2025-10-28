package payment_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/voucher"
)

type voucherStubQueries struct {
	voucher      dbgen.Voucher
	inserted     int
	usageChecked bool
}

func (q *voucherStubQueries) GetVoucherByCodeForUpdate(ctx context.Context, code string) (dbgen.Voucher, error) {
	return q.voucher, nil
}

func (q *voucherStubQueries) CountVoucherUsageByUser(ctx context.Context, arg dbgen.CountVoucherUsageByUserParams) (int64, error) {
	return 0, nil
}

func (q *voucherStubQueries) GetVoucherUsageByOrder(ctx context.Context, arg dbgen.GetVoucherUsageByOrderParams) (dbgen.VoucherUsage, error) {
	if q.usageChecked {
		return dbgen.VoucherUsage{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}}, nil
	}
	q.usageChecked = true
	return dbgen.VoucherUsage{}, pgx.ErrNoRows
}

func (q *voucherStubQueries) InsertVoucherUsage(ctx context.Context, arg dbgen.InsertVoucherUsageParams) error {
	q.inserted++
	return nil
}

func (q *voucherStubQueries) IncreaseVoucherUsedCount(ctx context.Context, id pgtype.UUID) error {
	return nil
}

func TestVoucherSettlementIdempotent(t *testing.T) {
	v := dbgen.Voucher{ID: uuidToPg(uuid.New()), Code: "PROMO", MinSpend: 0, Value: 10_000, Kind: dbgen.DiscountKindFixedAmount,
		ValidFrom: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}, ValidTo: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}}
	stub := &voucherStubQueries{voucher: v}
	svc := &voucher.Service{Q: stub, DefaultPerUserLimit: 0}
	orderID := uuidToPg(uuid.New())
	userID := uuidToPg(uuid.New())
	if err := svc.Settle(context.Background(), "PROMO", orderID, userID, 5_000); err != nil {
		t.Fatalf("first settlement failed: %v", err)
	}
	if err := svc.Settle(context.Background(), "PROMO", orderID, userID, 5_000); err != nil {
		t.Fatalf("second settlement failed: %v", err)
	}
	if stub.inserted != 1 {
		t.Fatalf("expected 1 insert, got %d", stub.inserted)
	}
}

func uuidToPg(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
