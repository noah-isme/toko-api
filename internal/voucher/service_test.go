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
	asked      []dbgen.GetVoucherByCodeForUpdateParams
}

// GetVoucherByCodeForUpdate mirrors the tenant-scoped SQL: codes are unique per
// tenant, so a voucher of another tenant is not found.
func (s *stubQueries) GetVoucherByCodeForUpdate(ctx context.Context, arg dbgen.GetVoucherByCodeForUpdateParams) (dbgen.Voucher, error) {
	s.asked = append(s.asked, arg)
	if s.voucher.Code == "" || s.voucher.Code != arg.Code || s.voucher.TenantID != arg.TenantID {
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
	v := newVoucher(1000, 2, 0)
	svc := &Service{Q: &stubQueries{voucher: v}, DefaultPerUserLimit: 1}
	_, err := svc.Preview(context.Background(), v.TenantID, "PROMO", nil, 500, nil)
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
	_, err := svc.Preview(context.Background(), v.TenantID, "PROMO", &userID, 10_000, []Item{{Subtotal: 10_000}})
	if err == nil {
		t.Fatal("expected error due to per user limit")
	}
}

func newVoucher(value int64, usedCount int32, percent int32) dbgen.Voucher {
	return dbgen.Voucher{
		ID:        uuidToPg(uuid.New()),
		TenantID:  uuidToPg(uuid.New()),
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

func TestSettleResolvesCodeInOrderTenantOnly(t *testing.T) {
	v := newVoucher(1000, 0, 0) // owned by another tenant
	stub := &stubQueries{voucher: v}
	svc := &Service{Q: stub}
	orderTenant := uuidToPg(uuid.New())

	if err := svc.Settle(context.Background(), orderTenant, "PROMO", uuidToPg(uuid.New()), pgtype.UUID{}, 500); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if stub.inserted != 0 {
		t.Fatalf("another tenant's voucher must not be settled, got %d usage rows", stub.inserted)
	}
	if len(stub.asked) != 1 || stub.asked[0].TenantID != orderTenant {
		t.Fatalf("code must be resolved in the order's tenant, asked %+v", stub.asked)
	}
}

func TestSettleWithoutTenantLogsAndSkips(t *testing.T) {
	stub := &stubQueries{voucher: newVoucher(1000, 0, 0)}
	svc := &Service{Q: stub}
	var buf bytes.Buffer
	ctx := zerolog.New(&buf).WithContext(context.Background())
	orderID := uuidToPg(uuid.New())

	if err := svc.Settle(ctx, pgtype.UUID{}, "PROMO", orderID, pgtype.UUID{}, 500); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if stub.inserted != 0 || len(stub.asked) != 0 {
		t.Fatalf("an order without tenant must not touch vouchers (inserted=%d asked=%d)", stub.inserted, len(stub.asked))
	}
	if out := buf.String(); !strings.Contains(out, "order has no tenant") || !strings.Contains(out, uuid.UUID(orderID.Bytes).String()) {
		t.Fatalf("expected a warning naming the order, got %q", out)
	}
}

func TestPreviewResolvesCodeInRequestTenantOnly(t *testing.T) {
	stub := &stubQueries{voucher: newVoucher(1000, 0, 0)}
	svc := &Service{Q: stub}
	other := uuidToPg(uuid.New())
	if _, err := svc.Preview(context.Background(), other, "PROMO", nil, 10_000, []Item{{Subtotal: 10_000}}); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("another tenant's code must read as not eligible, got %v", err)
	}
	if len(stub.asked) != 1 || stub.asked[0].TenantID != other {
		t.Fatalf("code must be resolved in the request tenant, asked %+v", stub.asked)
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
