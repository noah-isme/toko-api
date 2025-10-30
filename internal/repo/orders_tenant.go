package repo

import (
	"context"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// OrdersTenantQuerier defines the sqlc generated queries used by OrdersTenantRepo.
type OrdersTenantQuerier interface {
	ListOrdersByTenant(ctx context.Context, arg dbgen.ListOrdersByTenantParams) ([]dbgen.ListOrdersByTenantRow, error)
	GetOrderByTenant(ctx context.Context, arg dbgen.GetOrderByTenantParams) (dbgen.GetOrderByTenantRow, error)
}

// OrdersTenantRepo ensures tenant scoping is applied to order queries.
type OrdersTenantRepo struct {
	Q OrdersTenantQuerier
}

// List returns tenant filtered orders optionally narrowed by status.
func (r OrdersTenantRepo) List(ctx context.Context, status *string, limit, offset int32) ([]dbgen.ListOrdersByTenantRow, error) {
	tid, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return nil, err
	}
	params := dbgen.ListOrdersByTenantParams{
		TenantID:    tid,
		Status:      status,
		LimitValue:  limit,
		OffsetValue: offset,
	}
	return r.Q.ListOrdersByTenant(ctx, params)
}

// Get returns a tenant scoped order by id.
func (r OrdersTenantRepo) Get(ctx context.Context, id string) (dbgen.GetOrderByTenantRow, error) {
	tid, err := tenantUUIDFromContext(ctx)
	if err != nil {
		return dbgen.GetOrderByTenantRow{}, err
	}
	orderID, err := uuidValue(id)
	if err != nil {
		return dbgen.GetOrderByTenantRow{}, err
	}
	params := dbgen.GetOrderByTenantParams{
		TenantID: tid,
		ID:       orderID,
	}
	return r.Q.GetOrderByTenant(ctx, params)
}
