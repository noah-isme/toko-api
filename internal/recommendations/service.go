package recommendations

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Service orchestrates recommendation queries.
type Service struct {
	queries queryProvider
}

// queryProvider interface defines the DB methods we need.
type queryProvider interface {
	ListProductsPublic(ctx context.Context, arg dbgen.ListProductsPublicParams) ([]dbgen.ListProductsPublicRow, error)
	GetProductBySlug(ctx context.Context, arg dbgen.GetProductBySlugParams) (dbgen.GetProductBySlugRow, error)
	GetProductForCart(ctx context.Context, arg dbgen.GetProductForCartParams) (dbgen.GetProductForCartRow, error)
	GetFrequentlyBoughtTogether(ctx context.Context, arg dbgen.GetFrequentlyBoughtTogetherParams) ([]dbgen.GetFrequentlyBoughtTogetherRow, error)
	GetCustomersAlsoViewed(ctx context.Context, arg dbgen.GetCustomersAlsoViewedParams) ([]dbgen.GetCustomersAlsoViewedRow, error)
	GetPersonalizedRecommendations(ctx context.Context, arg dbgen.GetPersonalizedRecommendationsParams) ([]dbgen.GetPersonalizedRecommendationsRow, error)
	GetTrendingProducts(ctx context.Context, arg dbgen.GetTrendingProductsParams) ([]dbgen.GetTrendingProductsRow, error)
	UpsertUserProductView(ctx context.Context, arg dbgen.UpsertUserProductViewParams) (dbgen.UserProductView, error)
	UpsertOrderProductPair(ctx context.Context, arg dbgen.UpsertOrderProductPairParams) (dbgen.OrderProductPair, error)
}

// ServiceConfig groups Service dependencies.
type ServiceConfig struct {
	Queries queryProvider
}

// ProductListItem represents a product in recommendation responses.
type ProductListItem struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Slug      string  `json:"slug"`
	Price     int64   `json:"price"`
	Thumbnail *string `json:"thumbnail,omitempty"`
	Rating    float64 `json:"rating"`
	InStock   bool    `json:"inStock"`
}

// NewService constructs a Service instance.
func NewService(cfg ServiceConfig) (*Service, error) {
	if cfg.Queries == nil {
		return nil, errors.New("recommendations: queries provider is required")
	}
	return &Service{queries: cfg.Queries}, nil
}

// Personalized returns personalized product recommendations for the authenticated user.
func (s *Service) Personalized(ctx context.Context, limit int) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)
	userID := userIDFromContext(ctx)

	if !userID.Valid {
		// Fallback to trending for anonymous users
		return s.Trending(ctx, limit)
	}

	rows, err := s.queries.GetPersonalizedRecommendations(ctx, dbgen.GetPersonalizedRecommendationsParams{
		TenantID: tenantID,
		UserID:   userID,
		Limit:    int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("get personalized recommendations: %w", err)
	}

	return mapPersonalizedRows(rows), nil
}

// Trending returns trending/popular products across the platform.
func (s *Service) Trending(ctx context.Context, limit int) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)
	rows, err := s.queries.GetTrendingProducts(ctx, dbgen.GetTrendingProductsParams{
		TenantID: tenantID,
		Limit:    int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list trending products: %w", err)
	}
	return mapTrendingRows(rows), nil
}

// FrequentlyBoughtTogether returns products frequently bought together with the given product.
func (s *Service) FrequentlyBoughtTogether(ctx context.Context, productID string) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)

	// Resolve product reference (UUID or slug)
	resolvedProductID, err := s.resolveProductRef(ctx, tenantID, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []ProductListItem{}, nil
		}
		return nil, fmt.Errorf("resolve product reference: %w", err)
	}

	rows, err := s.queries.GetFrequentlyBoughtTogether(ctx, dbgen.GetFrequentlyBoughtTogetherParams{
		TenantID:   tenantID,
		ProductIDA: resolvedProductID,
		Limit:      10,
	})
	if err != nil {
		return nil, fmt.Errorf("get frequently bought together: %w", err)
	}

	return mapFBTRows(rows), nil
}

// CustomersAlsoViewed returns products other customers viewed after viewing the given product.
func (s *Service) CustomersAlsoViewed(ctx context.Context, productID string) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)

	// Resolve product reference (UUID or slug)
	resolvedProductID, err := s.resolveProductRef(ctx, tenantID, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []ProductListItem{}, nil
		}
		return nil, fmt.Errorf("resolve product reference: %w", err)
	}

	rows, err := s.queries.GetCustomersAlsoViewed(ctx, dbgen.GetCustomersAlsoViewedParams{
		TenantID:  tenantID,
		ProductID: resolvedProductID,
		Limit:     10,
	})
	if err != nil {
		return nil, fmt.Errorf("get customers also viewed: %w", err)
	}

	return mapCAVRows(rows), nil
}

// TrackProductView records a user's product view for recommendation tracking.
func (s *Service) TrackProductView(ctx context.Context, userID pgtype.UUID, productID pgtype.UUID) error {
	tenantID := tenantIDFromContext(ctx)
	if !tenantID.Valid {
		return nil // Skip tracking if no tenant
	}
	_, err := s.queries.UpsertUserProductView(ctx, dbgen.UpsertUserProductViewParams{
		TenantID:  tenantID,
		UserID:    userID,
		ProductID: productID,
	})
	if err != nil {
		return fmt.Errorf("upsert user product view: %w", err)
	}
	return nil
}

// TrackOrderProducts records product pairs from an order for FBT recommendations.
func (s *Service) TrackOrderProducts(ctx context.Context, orderID pgtype.UUID, productIDs []pgtype.UUID) error {
	tenantID := tenantIDFromContext(ctx)
	if !tenantID.Valid {
		return nil
	}

	// Create pairs from all products in the order
	for i := 0; i < len(productIDs); i++ {
		for j := i + 1; j < len(productIDs); j++ {
			_, err := s.queries.UpsertOrderProductPair(ctx, dbgen.UpsertOrderProductPairParams{
				TenantID: tenantID,
				Column2:  productIDs[i],
				Column3:  productIDs[j],
			})
			if err != nil {
				return fmt.Errorf("upsert order product pair: %w", err)
			}
		}
	}
	return nil
}

// resolveProductRef resolves a product reference (UUID or slug) to a product UUID.
func (s *Service) resolveProductRef(ctx context.Context, tenantID pgtype.UUID, ref string) (pgtype.UUID, error) {
	// Try as UUID first
	if parsed, err := uuid.Parse(ref); err == nil {
		_, err := s.queries.GetProductForCart(ctx, dbgen.GetProductForCartParams{
			ID:       pgtype.UUID{Bytes: parsed, Valid: true},
			TenantID: tenantID,
		})
		if err == nil {
			return pgtype.UUID{Bytes: parsed, Valid: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, err
		}
	}

	// Fallback to slug lookup
	product, err := s.queries.GetProductBySlug(ctx, dbgen.GetProductBySlugParams{
		Slug:     ref,
		TenantID: tenantID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return product.ID, nil
}

func mapPersonalizedRows(rows []dbgen.GetPersonalizedRecommendationsRow) []ProductListItem {
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapRecommendationRow(row.ID, row.Title, row.Slug, row.Price, row.Thumbnail, row.Rating, row.InStock))
	}
	return items
}

func mapTrendingRows(rows []dbgen.GetTrendingProductsRow) []ProductListItem {
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapRecommendationRow(row.ID, row.Title, row.Slug, row.Price, row.Thumbnail, row.Rating, row.InStock))
	}
	return items
}

func mapFBTRows(rows []dbgen.GetFrequentlyBoughtTogetherRow) []ProductListItem {
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapRecommendationRow(row.ID, row.Title, row.Slug, row.Price, row.Thumbnail, row.Rating, row.InStock))
	}
	return items
}

func mapCAVRows(rows []dbgen.GetCustomersAlsoViewedRow) []ProductListItem {
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapRecommendationRow(row.ID, row.Title, row.Slug, row.Price, row.Thumbnail, row.Rating, row.InStock))
	}
	return items
}

func mapRecommendationRow(id pgtype.UUID, title, slug string, price int64, thumbnail pgtype.Text, rating float64, inStock bool) ProductListItem {
	item := ProductListItem{
		ID:      uuidString(id),
		Title:   title,
		Slug:    slug,
		Price:   price,
		InStock: inStock,
		Rating:  rating,
	}
	if thumbnail.Valid {
		thumb := thumbnail.String
		item.Thumbnail = &thumb
	}
	return item
}

func mapProducts(rows []dbgen.ListProductsPublicRow) []ProductListItem {
	items := make([]ProductListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapProduct(row))
	}
	return items
}

func mapProduct(row dbgen.ListProductsPublicRow) ProductListItem {
	item := ProductListItem{
		ID:      uuidString(row.ID),
		Title:   row.Title,
		Slug:    row.Slug,
		Price:   row.Price,
		InStock: row.InStock,
		Rating:  row.Rating,
	}
	if row.Thumbnail.Valid {
		thumb := row.Thumbnail.String
		item.Thumbnail = &thumb
	}
	return item
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	u, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return ""
	}
	return u.String()
}

func tenantIDFromContext(ctx context.Context) pgtype.UUID {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}

// UserIDKey is the context key for user ID
type UserIDKey struct{}

func userIDFromContext(ctx context.Context) pgtype.UUID {
	if id, ok := ctx.Value(UserIDKey{}).(string); ok {
		if parsed, err := uuid.Parse(id); err == nil {
			return pgtype.UUID{Bytes: parsed, Valid: true}
		}
	}
	return pgtype.UUID{}
}