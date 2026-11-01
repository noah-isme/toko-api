package voucher

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type stubQueries struct {
	voucher    dbgen.Voucher
	usageCount int64
	usageErr   error
	inserted   int
}

func (s *stubQueries) GetVoucherByCodeForUpdate(ctx context.Context, code string) (dbgen.Voucher, error) {
	if s.voucher.Code == "" {
		return dbgen.Voucher{}, pgx.ErrNoRows
	}
	return s.voucher, nil
}

func (s *stubQueries) CountVoucherUsageByUser(ctx context.Context, arg dbgen.CountVoucherUsageByUserParams) (int64, error) {
	if s.usageErr != nil {
		return 0, s.usageErr
	}
	return s.usageCount, nil
}

func (s *stubQueries) GetVoucherUsageByOrder(ctx context.Context, arg dbgen.GetVoucherUsageByOrderParams) (dbgen.VoucherUsage, error) {
	return dbgen.VoucherUsage{}, pgx.ErrNoRows
}

func (s *stubQueries) InsertVoucherUsage(ctx context.Context, arg dbgen.InsertVoucherUsageParams) error {
	s.inserted++
	return nil
}
func (s *stubQueries) IncreaseVoucherUsedCount(ctx context.Context, id pgtype.UUID) error { return nil }

func TestPreviewMinSpend(t *testing.T) {
	svc := &Service{Q: &stubQueries{voucher: newVoucher(1000, 2, 0)}, DefaultPerUserLimit: 1}
	_, err := svc.Preview(context.Background(), "PROMO", nil, 500, nil)
	if !errors.Is(err, ErrMinimumSpendUnmet) {
		t.Fatalf("expected ErrMinimumSpendUnmet, got %v", err)
	}
}

func TestPerUserLimit(t *testing.T) {
	v := newVoucher(1000, 2, 0)
	limit := int32(1)
	v.PerUserLimit = pgtype.Int4{Int32: limit, Valid: true}
	userID := uuid.New().String()
	svc := &Service{Q: &stubQueries{voucher: v, usageCount: 1}, DefaultPerUserLimit: 1}
	_, err := svc.Preview(context.Background(), "PROMO", &userID, 10_000, []Item{{Subtotal: 10_000}})
	if err == nil {
		t.Fatal("expected error due to per user limit")
	}
}

func newVoucher(value int64, usedCount int32, percent int32) dbgen.Voucher {
	return dbgen.Voucher{
		ID:        uuidToPg(uuid.New()),
		Code:      "PROMO",
		Value:     value,
		MinSpend:  1_000,
		UsedCount: usedCount,
		Kind:      dbgen.DiscountKindFixedAmount,
		ValidFrom: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
		ValidTo:   pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}
}

func uuidToPg(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func TestSettleSkipsAndLogsForeignTenantVoucher(t *testing.T) {
	v := newVoucher(1000, 0, 0)
	v.TenantID = uuidToPg(uuid.New())
	stub := &stubQueries{voucher: v}
	svc := &Service{Q: stub}

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	ctx := logger.WithContext(context.Background())
	orderTenant := uuidToPg(uuid.New())
	orderID := uuidToPg(uuid.New())

	if err := svc.Settle(ctx, orderTenant, "PROMO", orderID, pgtype.UUID{}, 500); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if stub.inserted != 0 {
		t.Fatalf("foreign voucher must not be settled, got %d usage rows", stub.inserted)
	}
	out := buf.String()
	if !strings.Contains(out, "voucher belongs to another tenant") || !strings.Contains(out, uuid.UUID(orderID.Bytes).String()) {
		t.Fatalf("expected a warning naming the order, got %q", out)
	}
}

func TestSettleRecordsOwnTenantVoucher(t *testing.T) {
	v := newVoucher(1000, 0, 0)
	tenantID := uuidToPg(uuid.New())
	v.TenantID = tenantID
	stub := &stubQueries{voucher: v}
	svc := &Service{Q: stub}
	if err := svc.Settle(context.Background(), tenantID, "PROMO", uuidToPg(uuid.New()), pgtype.UUID{}, 500); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if stub.inserted != 1 {
		t.Fatalf("expected one usage row, got %d", stub.inserted)
	}
}
