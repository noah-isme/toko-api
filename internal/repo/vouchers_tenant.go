package repo

import (
	"context"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// VouchersTenantQuerier defines the sqlc generated queries used by VouchersTenantRepo.
type VouchersTenantQuerier interface {
	GetVoucherByTenant(ctx context.Context, arg dbgen.GetVoucherByTenantParams) (dbgen.GetVoucherByTenantRow, error)
}

// VouchersTenantRepo ensures tenant scoping is applied to voucher queries.
type VouchersTenantRepo struct {
	Q VouchersTenantQuerier
}

// Get retrieves a voucher by code scoped to the tenant in context.
func (r VouchersTenantRepo) Get(ctx context.Context, code string) (dbgen.GetVoucherByTenantRow, error) {
	tid, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return dbgen.GetVoucherByTenantRow{}, err
	}
	params := dbgen.GetVoucherByTenantParams{
		TenantID: tid,
		Code:     code,
	}
	return r.Q.GetVoucherByTenant(ctx, params)
}
