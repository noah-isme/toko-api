package repo

import (
	"context"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// ProductsTenantQuerier defines the sqlc generated queries used by ProductsTenantRepo.
type ProductsTenantQuerier interface {
	ListProductsByTenant(ctx context.Context, arg dbgen.ListProductsByTenantParams) ([]dbgen.ListProductsByTenantRow, error)
	GetProductDetailByTenant(ctx context.Context, arg dbgen.GetProductDetailByTenantParams) (dbgen.GetProductDetailByTenantRow, error)
}

// ProductsTenantRepo ensures tenant scoping is applied to product queries.
type ProductsTenantRepo struct {
	Q ProductsTenantQuerier
}

// List returns paginated tenant specific products.
func (r ProductsTenantRepo) List(ctx context.Context, limit, offset int32) ([]dbgen.ListProductsByTenantRow, error) {
	tid, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	params := dbgen.ListProductsByTenantParams{
		TenantID:    tid,
		LimitValue:  limit,
		OffsetValue: offset,
	}
	return r.Q.ListProductsByTenant(ctx, params)
}

// GetDetail returns a single tenant scoped product by slug.
func (r ProductsTenantRepo) GetDetail(ctx context.Context, slug string) (dbgen.GetProductDetailByTenantRow, error) {
	tid, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return dbgen.GetProductDetailByTenantRow{}, err
	}
	params := dbgen.GetProductDetailByTenantParams{
		TenantID: tid,
		Slug:     slug,
	}
	return r.Q.GetProductDetailByTenant(ctx, params)
}
