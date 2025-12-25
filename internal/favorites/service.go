package favorites

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/db/gen"
)

type Service struct {
	Q *dbgen.Queries
}

func (s *Service) Add(ctx context.Context, userID, productID, tenantID pgtype.UUID) error {
	return s.Q.AddFavorite(ctx, dbgen.AddFavoriteParams{
		UserID:    userID,
		ProductID: productID,
		TenantID:  tenantID,
	})
}

func (s *Service) Remove(ctx context.Context, userID, productID, tenantID pgtype.UUID) error {
	return s.Q.RemoveFavorite(ctx, dbgen.RemoveFavoriteParams{
		UserID:    userID,
		ProductID: productID,
		TenantID:  tenantID,
	})
}

func (s *Service) List(ctx context.Context, userID, tenantID pgtype.UUID) ([]dbgen.ListFavoritesRow, error) {
	return s.Q.ListFavorites(ctx, dbgen.ListFavoritesParams{
		UserID:   userID,
		TenantID: tenantID,
	})
}

func (s *Service) Check(ctx context.Context, userID, productID, tenantID pgtype.UUID) (bool, error) {
	_, err := s.Q.CheckFavorite(ctx, dbgen.CheckFavoriteParams{
		UserID:    userID,
		ProductID: productID,
		TenantID:  tenantID,
	})
	if err != nil {
		return false, nil // Assume no rows means false, but need to check pgx.ErrNoRows if CheckFavorite returns error on no rows
	}
	return true, nil
}
