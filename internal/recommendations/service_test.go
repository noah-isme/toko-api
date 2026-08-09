package recommendations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockQueries implements the queryProvider interface for testing
type MockQueries struct {
	mock.Mock
}

func (m *MockQueries) GetProductBySlug(ctx context.Context, arg dbgen.GetProductBySlugParams) (dbgen.GetProductBySlugRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(dbgen.GetProductBySlugRow), args.Error(1)
}

func (m *MockQueries) GetProductForCart(ctx context.Context, arg dbgen.GetProductForCartParams) (dbgen.GetProductForCartRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(dbgen.GetProductForCartRow), args.Error(1)
}

func (m *MockQueries) GetFrequentlyBoughtTogether(ctx context.Context, arg dbgen.GetFrequentlyBoughtTogetherParams) ([]dbgen.GetFrequentlyBoughtTogetherRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).([]dbgen.GetFrequentlyBoughtTogetherRow), args.Error(1)
}

func (m *MockQueries) GetCustomersAlsoViewed(ctx context.Context, arg dbgen.GetCustomersAlsoViewedParams) ([]dbgen.GetCustomersAlsoViewedRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).([]dbgen.GetCustomersAlsoViewedRow), args.Error(1)
}

func (m *MockQueries) GetPersonalizedRecommendations(ctx context.Context, arg dbgen.GetPersonalizedRecommendationsParams) ([]dbgen.GetPersonalizedRecommendationsRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).([]dbgen.GetPersonalizedRecommendationsRow), args.Error(1)
}

func (m *MockQueries) GetTrendingProducts(ctx context.Context, arg dbgen.GetTrendingProductsParams) ([]dbgen.GetTrendingProductsRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).([]dbgen.GetTrendingProductsRow), args.Error(1)
}

func (m *MockQueries) UpsertUserProductView(ctx context.Context, arg dbgen.UpsertUserProductViewParams) (dbgen.UserProductView, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(dbgen.UserProductView), args.Error(1)
}

func (m *MockQueries) UpsertOrderProductPair(ctx context.Context, arg dbgen.UpsertOrderProductPairParams) (dbgen.OrderProductPair, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(dbgen.OrderProductPair), args.Error(1)
}

func TestService_Personalized_AuthenticatedUser(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	userID := pgtype.UUID{Bytes: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Valid: true}
	productID := pgtype.UUID{Bytes: uuid.MustParse("33333333-3333-3333-3333-333333333333"), Valid: true}

	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())
	ctx = context.WithValue(ctx, UserIDKey{}, userID.String())

	expectedRows := []dbgen.GetPersonalizedRecommendationsRow{
		{
			ID:           productID,
			Title:        "Test Product",
			Slug:         "test-product",
			Price:        10000,
			InStock:      true,
			Rating:       4.5,
			Thumbnail:    pgtype.Text{String: "thumb.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  10,
			TotalStock:   5,
			Score:        2.0,
		},
	}

	mockQueries.On("GetPersonalizedRecommendations", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.Personalized(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "Test Product", items[0].Title)
	assert.Equal(t, "test-product", items[0].Slug)
	assert.Equal(t, int64(10000), items[0].Price)
	assert.Equal(t, 4.5, items[0].Rating)
	assert.True(t, items[0].InStock)

	mockQueries.AssertExpectations(t)
}

func TestService_Personalized_AnonymousUser_FallbacksToTrending(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}

	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())
	// No user ID in context = anonymous

	expectedRows := []dbgen.GetTrendingProductsRow{
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("44444444-4444-4444-4444-444444444444"), Valid: true},
			Title:        "Trending Product",
			Slug:         "trending-product",
			Price:        20000,
			InStock:      true,
			Rating:       4.8,
			Thumbnail:    pgtype.Text{String: "trending.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  20,
			TotalStock:   3,
		},
	}

	mockQueries.On("GetTrendingProducts", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.Personalized(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "Trending Product", items[0].Title)

	mockQueries.AssertExpectations(t)
}

func TestService_Trending(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	expectedRows := []dbgen.GetTrendingProductsRow{
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("55555555-5555-5555-5555-555555555555"), Valid: true},
			Title:        "Trending Item",
			Slug:         "trending-item",
			Price:        15000,
			InStock:      true,
			Rating:       4.7,
			Thumbnail:    pgtype.Text{String: "item.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  15,
			TotalStock:   10,
		},
	}

	mockQueries.On("GetTrendingProducts", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.Trending(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "Trending Item", items[0].Title)

	mockQueries.AssertExpectations(t)
}

func TestService_FrequentlyBoughtTogether_WithUUID(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	productID := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	expectedRows := []dbgen.GetFrequentlyBoughtTogetherRow{
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("77777777-7777-7777-7777-777777777777"), Valid: true},
			Title:        "FBT Product",
			Slug:         "fbt-product",
			Price:        25000,
			InStock:      true,
			Rating:       4.6,
			Thumbnail:    pgtype.Text{String: "fbt.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  8,
			TotalStock:   7,
			PairCount:    50,
		},
	}

	mockQueries.On("GetProductForCart", mock.Anything, mock.Anything).Return(dbgen.GetProductForCartRow{
		ID: pgtype.UUID{Bytes: productID, Valid: true},
	}, nil).Once()

	mockQueries.On("GetFrequentlyBoughtTogether", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.FrequentlyBoughtTogether(ctx, productID.String())
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "FBT Product", items[0].Title)

	mockQueries.AssertExpectations(t)
}

func TestService_FrequentlyBoughtTogether_WithSlug(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	expectedRows := []dbgen.GetFrequentlyBoughtTogetherRow{
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("88888888-8888-8888-8888-888888888888"), Valid: true},
			Title:        "FBT by Slug",
			Slug:         "fbt-by-slug",
			Price:        30000,
			InStock:      true,
			Rating:       4.4,
			Thumbnail:    pgtype.Text{String: "fbt2.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  12,
			TotalStock:   4,
			PairCount:    30,
		},
	}

	mockQueries.On("GetProductBySlug", mock.Anything, mock.Anything).Return(dbgen.GetProductBySlugRow{
		ID: pgtype.UUID{Bytes: uuid.MustParse("99999999-9999-9999-9999-999999999999"), Valid: true},
	}, nil).Once()

	mockQueries.On("GetFrequentlyBoughtTogether", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.FrequentlyBoughtTogether(ctx, "product-slug")
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "FBT by Slug", items[0].Title)

	mockQueries.AssertExpectations(t)
}

func TestService_CustomersAlsoViewed_WithUUID(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	productID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	expectedRows := []dbgen.GetCustomersAlsoViewedRow{
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"), Valid: true},
			Title:        "Also Viewed Product",
			Slug:         "also-viewed",
			Price:        18000,
			InStock:      true,
			Rating:       4.3,
			Thumbnail:    pgtype.Text{String: "also.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  5,
			TotalStock:   6,
		},
	}

	mockQueries.On("GetProductForCart", mock.Anything, mock.Anything).Return(dbgen.GetProductForCartRow{
		ID: pgtype.UUID{Bytes: productID, Valid: true},
	}, nil).Once()

	mockQueries.On("GetCustomersAlsoViewed", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.CustomersAlsoViewed(ctx, productID.String())
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "Also Viewed Product", items[0].Title)

	mockQueries.AssertExpectations(t)
}

func TestService_CustomersAlsoViewed_WithSlug(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	expectedRows := []dbgen.GetCustomersAlsoViewedRow{
		{
			ID:           pgtype.UUID{Bytes: uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc"), Valid: true},
			Title:        "CAV by Slug",
			Slug:         "cav-by-slug",
			Price:        22000,
			InStock:      true,
			Rating:       4.2,
			Thumbnail:    pgtype.Text{String: "cav.jpg", Valid: true},
			CategoryID:   pgtype.UUID{Valid: false},
			CategoryName: pgtype.Text{Valid: false},
			BrandID:      pgtype.UUID{Valid: false},
			BrandName:    pgtype.Text{Valid: false},
			CreatedAt:    pgtype.Timestamptz{Valid: false},
			ReviewCount:  7,
			TotalStock:   8,
		},
	}

	mockQueries.On("GetProductBySlug", mock.Anything, mock.Anything).Return(dbgen.GetProductBySlugRow{
		ID: pgtype.UUID{Bytes: uuid.MustParse("dddddddd-dddd-dddd-dddd-dddddddddddd"), Valid: true},
	}, nil).Once()

	mockQueries.On("GetCustomersAlsoViewed", mock.Anything, mock.Anything).Return(expectedRows, nil).Once()

	items, err := service.CustomersAlsoViewed(ctx, "product-slug-2")
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "CAV by Slug", items[0].Title)

	mockQueries.AssertExpectations(t)
}

func TestService_ResolveProductRef_NotFound(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	// Use a valid UUID format that doesn't exist in DB
	nonExistentUUID := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff").String()

	mockQueries.On("GetProductForCart", ctx, mock.Anything).Return(dbgen.GetProductForCartRow{}, pgx.ErrNoRows).Once()
	mockQueries.On("GetProductBySlug", ctx, mock.Anything).Return(dbgen.GetProductBySlugRow{}, pgx.ErrNoRows).Once()

	// Service returns empty list (not error) for not found products
	items, err := service.FrequentlyBoughtTogether(ctx, nonExistentUUID)
	require.NoError(t, err)
	assert.Empty(t, items)

	mockQueries.AssertExpectations(t)
}

func TestService_TrackProductView(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	userID := pgtype.UUID{Bytes: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Valid: true}
	productID := pgtype.UUID{Bytes: uuid.MustParse("33333333-3333-3333-3333-333333333333"), Valid: true}
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	mockQueries.On("UpsertUserProductView", mock.Anything, mock.Anything).Return(dbgen.UserProductView{}, nil).Once()

	err = service.TrackProductView(ctx, userID, productID)
	require.NoError(t, err)

	mockQueries.AssertExpectations(t)
}

func TestService_TrackOrderProducts(t *testing.T) {
	mockQueries := new(MockQueries)
	service, err := NewService(ServiceConfig{Queries: mockQueries})
	require.NoError(t, err)

	tenantID := pgtype.UUID{Bytes: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Valid: true}
	orderID := pgtype.UUID{Bytes: uuid.MustParse("44444444-4444-4444-4444-444444444444"), Valid: true}
	productIDs := []pgtype.UUID{
		{Bytes: uuid.MustParse("55555555-5555-5555-5555-555555555555"), Valid: true},
		{Bytes: uuid.MustParse("66666666-6666-6666-6666-666666666666"), Valid: true},
		{Bytes: uuid.MustParse("77777777-7777-7777-7777-777777777777"), Valid: true},
	}
	ctx := context.Background()
	ctx = tenant.WithTenant(ctx, tenantID.String())

	// 3 products = 3 pairs (0-1, 0-2, 1-2)
	mockQueries.On("UpsertOrderProductPair", mock.Anything, mock.Anything).Return(dbgen.OrderProductPair{}, nil).Times(3)

	err = service.TrackOrderProducts(ctx, orderID, productIDs)
	require.NoError(t, err)

	mockQueries.AssertExpectations(t)
}
