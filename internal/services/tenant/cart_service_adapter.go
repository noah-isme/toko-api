package tenantservices

import (
	"context"
	"errors"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

type CartID string

type CartRepo interface {
	Get(ctx context.Context, tenantID string, id CartID) (string, error)
	Create(ctx context.Context, tenantID string) (CartID, error)
}

type CartsService struct{ R CartRepo }

func (s CartsService) Get(ctx context.Context, id CartID) (string, error) {
	tid, ok := tenant.From(ctx)
	if !ok {
		return "", errors.New("tenant missing")
	}
	return s.R.Get(ctx, tid, id)
}

func (s CartsService) Create(ctx context.Context) (CartID, error) {
	tid, ok := tenant.From(ctx)
	if !ok {
		return "", errors.New("tenant missing")
	}
	return s.R.Create(ctx, tid)
}
