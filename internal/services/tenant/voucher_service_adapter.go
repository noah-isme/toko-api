package tenantservices

import (
	"context"
	"errors"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

type Voucher struct {
	ID   string
	Code string
}

type VoucherRepo interface {
	Get(ctx context.Context, tenantID string, code string) (Voucher, error)
}

type VouchersService struct{ R VoucherRepo }

func (s VouchersService) Get(ctx context.Context, code string) (Voucher, error) {
	tid, ok := tenant.From(ctx)
	if !ok {
		return Voucher{}, errors.New("tenant missing")
	}
	return s.R.Get(ctx, tid, code)
}
