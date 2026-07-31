package auth

// Admin* stubs keep fakeQueries satisfying dbgen.Querier. The auth package never
// calls the admin catalog/order queries, so each stub reports errNotImplemented.

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

func (f *fakeQueries) AdminCountCustomers(context.Context) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminCountOrders(context.Context, dbgen.AdminCountOrdersParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminCountProducts(context.Context, dbgen.AdminCountProductsParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminCountProductsTotal(context.Context) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminCountVouchers(context.Context, dbgen.AdminCountVouchersParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminCreateBrand(context.Context, dbgen.AdminCreateBrandParams) (dbgen.AdminCreateBrandRow, error) {
	return dbgen.AdminCreateBrandRow{}, errNotImplemented
}

func (f *fakeQueries) AdminCreateCategory(context.Context, dbgen.AdminCreateCategoryParams) (dbgen.AdminCreateCategoryRow, error) {
	return dbgen.AdminCreateCategoryRow{}, errNotImplemented
}

func (f *fakeQueries) AdminCreateProduct(context.Context, dbgen.AdminCreateProductParams) (pgtype.UUID, error) {
	return pgtype.UUID{}, errNotImplemented
}

func (f *fakeQueries) AdminDeleteBrand(context.Context, pgtype.UUID) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminDeleteCategory(context.Context, pgtype.UUID) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminDeleteProduct(context.Context, pgtype.UUID) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminDeleteProductSpecs(context.Context, pgtype.UUID) error {
	return errNotImplemented
}

func (f *fakeQueries) AdminDeleteProductVariant(context.Context, dbgen.AdminDeleteProductVariantParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminDeleteVoucher(context.Context, string) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) AdminGetOrder(context.Context, pgtype.UUID) (dbgen.AdminGetOrderRow, error) {
	return dbgen.AdminGetOrderRow{}, errNotImplemented
}

func (f *fakeQueries) AdminGetPrimaryVariant(context.Context, pgtype.UUID) (dbgen.ProductVariant, error) {
	return dbgen.ProductVariant{}, errNotImplemented
}

func (f *fakeQueries) AdminGetProduct(context.Context, pgtype.UUID) (dbgen.AdminGetProductRow, error) {
	return dbgen.AdminGetProductRow{}, errNotImplemented
}

func (f *fakeQueries) AdminGetProductIDBySlug(context.Context, string) (pgtype.UUID, error) {
	return pgtype.UUID{}, errNotImplemented
}

func (f *fakeQueries) AdminInsertProductImage(context.Context, dbgen.AdminInsertProductImageParams) error {
	return errNotImplemented
}

func (f *fakeQueries) AdminInsertProductSpec(context.Context, dbgen.AdminInsertProductSpecParams) error {
	return errNotImplemented
}

func (f *fakeQueries) AdminInsertProductVariant(context.Context, dbgen.AdminInsertProductVariantParams) (pgtype.UUID, error) {
	return pgtype.UUID{}, errNotImplemented
}

func (f *fakeQueries) AdminListBrands(context.Context) ([]dbgen.AdminListBrandsRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) AdminListCategories(context.Context) ([]dbgen.AdminListCategoriesRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) AdminListOrders(context.Context, dbgen.AdminListOrdersParams) ([]dbgen.AdminListOrdersRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) AdminListProducts(context.Context, dbgen.AdminListProductsParams) ([]dbgen.AdminListProductsRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) AdminListVouchers(context.Context, dbgen.AdminListVouchersParams) ([]dbgen.AdminListVouchersRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) AdminOrderStats(context.Context, dbgen.AdminOrderStatsParams) (dbgen.AdminOrderStatsRow, error) {
	return dbgen.AdminOrderStatsRow{}, errNotImplemented
}

func (f *fakeQueries) AdminReplaceProductImages(context.Context, pgtype.UUID) error {
	return errNotImplemented
}

func (f *fakeQueries) AdminSetProductStockFlag(context.Context, dbgen.AdminSetProductStockFlagParams) error {
	return errNotImplemented
}

func (f *fakeQueries) AdminTopProductsByRevenue(context.Context, dbgen.AdminTopProductsByRevenueParams) ([]dbgen.AdminTopProductsByRevenueRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) AdminUpdateBrand(context.Context, dbgen.AdminUpdateBrandParams) (dbgen.AdminUpdateBrandRow, error) {
	return dbgen.AdminUpdateBrandRow{}, errNotImplemented
}

func (f *fakeQueries) AdminUpdateCategory(context.Context, dbgen.AdminUpdateCategoryParams) (dbgen.AdminUpdateCategoryRow, error) {
	return dbgen.AdminUpdateCategoryRow{}, errNotImplemented
}

func (f *fakeQueries) AdminUpdateProduct(context.Context, dbgen.AdminUpdateProductParams) (pgtype.UUID, error) {
	return pgtype.UUID{}, errNotImplemented
}

func (f *fakeQueries) AdminUpdateProductVariant(context.Context, dbgen.AdminUpdateProductVariantParams) (dbgen.ProductVariant, error) {
	return dbgen.ProductVariant{}, errNotImplemented
}

func (f *fakeQueries) AdminVoucherStats(context.Context) (dbgen.AdminVoucherStatsRow, error) {
	return dbgen.AdminVoucherStatsRow{}, errNotImplemented
}
