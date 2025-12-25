package reviews

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/noah-isme/backend-toko/internal/db/gen"
)

type Service struct {
	Q *dbgen.Queries
}

func (s *Service) Create(ctx context.Context, userID, productID, tenantID pgtype.UUID, rating int32, comment string) (dbgen.Review, error) {
	return s.Q.CreateReview(ctx, dbgen.CreateReviewParams{
		ProductID: productID,
		UserID:    userID,
		Rating:    rating,
		Comment:   pgtype.Text{String: comment, Valid: comment != ""},
		TenantID:  tenantID,
	})
}

func (s *Service) List(ctx context.Context, productID, tenantID pgtype.UUID, page, limit int32) ([]dbgen.Review, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 10
	}
	offset := (page - 1) * limit
	return s.Q.GetProductReviews(ctx, dbgen.GetProductReviewsParams{
		ProductID: productID,
		TenantID:  tenantID,
		Limit:     limit,
		Offset:    offset,
	})
}

func (s *Service) Stats(ctx context.Context, productID, tenantID pgtype.UUID) (dbgen.GetReviewStatsRow, error) {
	return s.Q.GetReviewStats(ctx, dbgen.GetReviewStatsParams{
		ProductID: productID,
		TenantID:  tenantID,
	})
}

func (s *Service) Delete(ctx context.Context, reviewID, userID, tenantID pgtype.UUID) error {
	return s.Q.DeleteReview(ctx, dbgen.DeleteReviewParams{
		ID:       reviewID,
		UserID:   userID,
		TenantID: tenantID,
	})
}

func (s *Service) CheckUserReview(ctx context.Context, userID, productID, tenantID pgtype.UUID) (pgtype.UUID, error) {
	return s.Q.CheckUserReview(ctx, dbgen.CheckUserReviewParams{
		UserID:    userID,
		ProductID: productID,
		TenantID:  tenantID,
	})
}


