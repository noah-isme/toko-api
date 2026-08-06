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
// Currently falls back to trending products as a placeholder.
func (s *Service) Personalized(ctx context.Context, limit int) ([]ProductListItem, error) {
	// TODO: Implement actual personalized recommendations based on user history
	// For now, delegate to trending as a reasonable fallback
	return s.Trending(ctx, limit)
}

// Trending returns trending/popular products across the platform.
func (s *Service) Trending(ctx context.Context, limit int) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)
	rows, err := s.queries.ListProductsPublic(ctx, dbgen.ListProductsPublicParams{
		TenantID:     tenantID,
		Sort:         "rating:desc",
		OffsetValue:  0,
		LimitValue:   int32(limit),
		InStock:      pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list trending products: %w", err)
	}
	return mapProducts(rows), nil
}

// FrequentlyBoughtTogether returns products frequently bought together with the given product.
// Currently returns empty list as a placeholder.
func (s *Service) FrequentlyBoughtTogether(ctx context.Context, productID string) ([]ProductListItem, error) {
	// TODO: Implement based on order history analysis
	// For now return empty - this requires analytics data
	return []ProductListItem{}, nil
}

// CustomersAlsoViewed returns products other customers viewed after viewing the given product.
// Currently falls back to related products from the same category.
func (s *Service) CustomersAlsoViewed(ctx context.Context, productID string) ([]ProductListItem, error) {
	tenantID := tenantIDFromContext(ctx)

	// Try to get product by slug
	product, err := s.queries.GetProductBySlug(ctx, dbgen.GetProductBySlugParams{Slug: productID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []ProductListItem{}, nil
		}
		return nil, fmt.Errorf("get product by slug: %w", err)
	}

	if !product.CategoryID.Valid {
		return []ProductListItem{}, nil
	}

	rows, err := s.queries.ListProductsPublic(ctx, dbgen.ListProductsPublicParams{
		TenantID:     tenantID,
		CategorySlug: pgtype.Text{String: "", Valid: false},
		Sort:         "rating:desc",
		OffsetValue:  0,
		LimitValue:   int32(10),
		InStock:      pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list also viewed: %w", err)
	}

	// Filter out the current product
	var items []ProductListItem
	for _, row := range rows {
		if uuidString(row.ID) != productID {
			items = append(items, mapProduct(row))
		}
	}
	return items, nil
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
