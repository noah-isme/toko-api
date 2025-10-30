package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/repo"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type productsStub struct {
	listParams   dbgen.ListProductsByTenantParams
	detailParams dbgen.GetProductDetailByTenantParams
	listCalled   int
	detailCalled int
}

func (p *productsStub) ListProductsByTenant(ctx context.Context, arg dbgen.ListProductsByTenantParams) ([]dbgen.ListProductsByTenantRow, error) {
	p.listCalled++
	p.listParams = arg
	return []dbgen.ListProductsByTenantRow{{Slug: "sample"}}, nil
}

func (p *productsStub) GetProductDetailByTenant(ctx context.Context, arg dbgen.GetProductDetailByTenantParams) (dbgen.GetProductDetailByTenantRow, error) {
	p.detailCalled++
	p.detailParams = arg
	return dbgen.GetProductDetailByTenantRow{Slug: arg.Slug}, nil
}

func TestProductsTenantRepoRequiresTenant(t *testing.T) {
	tenantRepo := repo.ProductsTenantRepo{Q: &productsStub{}}
	if _, err := tenantRepo.List(context.Background(), 10, 0); !errors.Is(err, repo.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}
	if _, err := tenantRepo.GetDetail(context.Background(), "slug"); !errors.Is(err, repo.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}
}

func TestProductsTenantRepoDelegates(t *testing.T) {
	stub := &productsStub{}
	tenantRepo := repo.ProductsTenantRepo{Q: stub}
	tenantID := uuid.New().String()
	ctx := tenant.With(context.Background(), tenantID)
	rows, err := tenantRepo.List(ctx, 15, 5)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if stub.listCalled != 1 {
		t.Fatalf("expected list to be called once")
	}
	expectedUUID := uuid.MustParse(tenantID)
	if stub.listParams.TenantID.Bytes != expectedUUID {
		t.Fatalf("tenant mismatch in list: %v", stub.listParams.TenantID)
	}
	if stub.listParams.LimitValue != 15 || stub.listParams.OffsetValue != 5 {
		t.Fatalf("unexpected pagination params: %+v", stub.listParams)
	}
	if len(rows) != 1 || rows[0].Slug != "sample" {
		t.Fatalf("unexpected list result: %+v", rows)
	}
	row, err := tenantRepo.GetDetail(ctx, "sluggy")
	if err != nil {
		t.Fatalf("detail error: %v", err)
	}
	if stub.detailCalled != 1 {
		t.Fatalf("expected detail to be called once")
	}
	if stub.detailParams.TenantID.Bytes != expectedUUID {
		t.Fatalf("tenant mismatch in detail: %v", stub.detailParams.TenantID)
	}
	if stub.detailParams.Slug != "sluggy" || row.Slug != "sluggy" {
		t.Fatalf("unexpected slug in detail result: %+v", row)
	}
}

type ordersStub struct {
	listParams dbgen.ListOrdersByTenantParams
	getParams  dbgen.GetOrderByTenantParams
	listCalled int
	getCalled  int
}

func (o *ordersStub) ListOrdersByTenant(ctx context.Context, arg dbgen.ListOrdersByTenantParams) ([]dbgen.ListOrdersByTenantRow, error) {
	o.listCalled++
	o.listParams = arg
	return []dbgen.ListOrdersByTenantRow{{Status: dbgen.OrderStatus("PAID")}}, nil
}

func (o *ordersStub) GetOrderByTenant(ctx context.Context, arg dbgen.GetOrderByTenantParams) (dbgen.GetOrderByTenantRow, error) {
	o.getCalled++
	o.getParams = arg
	return dbgen.GetOrderByTenantRow{Status: dbgen.OrderStatus("PAID")}, nil
}

func TestOrdersTenantRepoRequiresTenant(t *testing.T) {
	tenantRepo := repo.OrdersTenantRepo{Q: &ordersStub{}}
	if _, err := tenantRepo.List(context.Background(), nil, 10, 0); !errors.Is(err, repo.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}
	if _, err := tenantRepo.Get(context.Background(), uuid.New().String()); !errors.Is(err, repo.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}
}

func TestOrdersTenantRepoDelegates(t *testing.T) {
	stub := &ordersStub{}
	tenantRepo := repo.OrdersTenantRepo{Q: stub}
	tenantID := uuid.New().String()
	ctx := tenant.With(context.Background(), tenantID)
	status := "PAID"
	rows, err := tenantRepo.List(ctx, &status, 25, 10)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if stub.listCalled != 1 {
		t.Fatalf("expected list to be called once")
	}
	expectedUUID := uuid.MustParse(tenantID)
	if stub.listParams.TenantID.Bytes != expectedUUID {
		t.Fatalf("tenant mismatch in orders list: %v", stub.listParams.TenantID)
	}
	if stub.listParams.Status != &status {
		t.Fatalf("status pointer mismatch: %#v", stub.listParams.Status)
	}
	if stub.listParams.LimitValue != 25 || stub.listParams.OffsetValue != 10 {
		t.Fatalf("unexpected pagination params: %+v", stub.listParams)
	}
	if len(rows) != 1 || rows[0].Status != dbgen.OrderStatus("PAID") {
		t.Fatalf("unexpected list rows: %+v", rows)
	}
	orderID := uuid.New()
	_, err = tenantRepo.Get(ctx, orderID.String())
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if stub.getCalled != 1 {
		t.Fatalf("expected get to be called once")
	}
	if stub.getParams.TenantID.Bytes != expectedUUID {
		t.Fatalf("tenant mismatch in get: %v", stub.getParams.TenantID)
	}
	if stub.getParams.ID.Bytes != orderID {
		t.Fatalf("order id mismatch: %v", stub.getParams.ID)
	}
}

type vouchersStub struct {
	getParams dbgen.GetVoucherByTenantParams
	called    int
}

func (v *vouchersStub) GetVoucherByTenant(ctx context.Context, arg dbgen.GetVoucherByTenantParams) (dbgen.GetVoucherByTenantRow, error) {
	v.called++
	v.getParams = arg
	return dbgen.GetVoucherByTenantRow{Code: arg.Code}, nil
}

func TestVouchersTenantRepoRequiresTenant(t *testing.T) {
	tenantRepo := repo.VouchersTenantRepo{Q: &vouchersStub{}}
	if _, err := tenantRepo.Get(context.Background(), "PROMO"); !errors.Is(err, repo.ErrTenantMissing) {
		t.Fatalf("expected ErrTenantMissing, got %v", err)
	}
}

func TestVouchersTenantRepoDelegates(t *testing.T) {
	stub := &vouchersStub{}
	tenantRepo := repo.VouchersTenantRepo{Q: stub}
	tenantID := uuid.New().String()
	ctx := tenant.With(context.Background(), tenantID)
	row, err := tenantRepo.Get(ctx, "PROMO")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if stub.called != 1 {
		t.Fatalf("expected get to be called once")
	}
	expectedUUID := uuid.MustParse(tenantID)
	if stub.getParams.TenantID.Bytes != expectedUUID {
		t.Fatalf("tenant mismatch in voucher get: %v", stub.getParams.TenantID)
	}
	if row.Code != "PROMO" {
		t.Fatalf("unexpected row code: %+v", row)
	}
}
