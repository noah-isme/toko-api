package tenantservices_test

import (
	"context"
	"errors"
	"testing"

	tenantservices "github.com/noah-isme/backend-toko/internal/services/tenant"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type fakeOrderRepo struct{}

func (fakeOrderRepo) Create(ctx context.Context, tenantID string, in tenantservices.OrderCreateInput) (tenantservices.Order, error) {
	if tenantID == "" {
		return tenantservices.Order{}, errors.New("no tenant")
	}
	return tenantservices.Order{ID: "o1", TenantID: tenantID, UserID: in.UserID}, nil
}

func (fakeOrderRepo) Get(ctx context.Context, tenantID, id string) (tenantservices.Order, error) {
	if tenantID == "" {
		return tenantservices.Order{}, errors.New("no tenant")
	}
	return tenantservices.Order{ID: id, TenantID: tenantID}, nil
}

func TestOrdersService_Create_MissingTenant(t *testing.T) {
	svc := tenantservices.OrdersService{R: fakeOrderRepo{}}
	if _, err := svc.Create(context.Background(), tenantservices.OrderCreateInput{UserID: "u1"}); err == nil {
		t.Fatal("expected error for missing tenant")
	}
}

func TestOrdersService_Create_WithTenant(t *testing.T) {
	svc := tenantservices.OrdersService{R: fakeOrderRepo{}}
	ctx := tenant.With(context.Background(), "t_1")
	ord, err := svc.Create(ctx, tenantservices.OrderCreateInput{UserID: "u1"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if ord.TenantID != "t_1" {
		t.Fatalf("wrong tenant: %s", ord.TenantID)
	}
}
