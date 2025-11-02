package tenantservices

import (
	"context"
	"errors"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

type OrderCreateInput struct {
	UserID string
	// other fields trimmed for brevity
}

type Order struct {
	ID       string
	TenantID string
	UserID   string
}

type OrderRepo interface {
	Create(ctx context.Context, tenantID string, in OrderCreateInput) (Order, error)
	Get(ctx context.Context, tenantID string, id string) (Order, error)
}

type OrdersService struct{ R OrderRepo }

func (s OrdersService) Create(ctx context.Context, in OrderCreateInput) (Order, error) {
	tid, ok := tenant.From(ctx)
	if !ok {
		return Order{}, errors.New("tenant missing")
	}
	return s.R.Create(ctx, tid, in)
}

func (s OrdersService) Get(ctx context.Context, id string) (Order, error) {
	tid, ok := tenant.From(ctx)
	if !ok {
		return Order{}, errors.New("tenant missing")
	}
	return s.R.Get(ctx, tid, id)
}
