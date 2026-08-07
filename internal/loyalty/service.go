package loyalty

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type Service struct {
	Q *dbgen.Queries
}

func (s *Service) GetProfile(ctx context.Context, userID pgtype.UUID) (dbgen.GetLoyaltyProfileRow, error) {
	return s.Q.GetLoyaltyProfile(ctx, userID)
}

func (s *Service) CreateOrUpdateProfile(ctx context.Context, userID pgtype.UUID, points, tierProgress, lifetimePoints int32, tier string) (dbgen.CreateOrUpdateLoyaltyProfileRow, error) {
	return s.Q.CreateOrUpdateLoyaltyProfile(ctx, dbgen.CreateOrUpdateLoyaltyProfileParams{
		UserID:         userID,
		Points:         points,
		Tier:           tier,
		TierProgress:   tierProgress,
		LifetimePoints: lifetimePoints,
	})
}

func (s *Service) GetTransactions(ctx context.Context, userID pgtype.UUID, page, limit int32) ([]dbgen.GetLoyaltyTransactionsRow, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 10
	}
	offset := (page - 1) * limit
	return s.Q.GetLoyaltyTransactions(ctx, dbgen.GetLoyaltyTransactionsParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
}

func (s *Service) CreateTransaction(ctx context.Context, userID pgtype.UUID, txType, description string, points, balance int32, referenceID pgtype.UUID, referenceType string) (dbgen.CreateLoyaltyTransactionRow, error) {
	return s.Q.CreateLoyaltyTransaction(ctx, dbgen.CreateLoyaltyTransactionParams{
		UserID:        userID,
		Type:          txType,
		Points:        points,
		Balance:       balance,
		Description:   description,
		ReferenceID:   referenceID,
		ReferenceType: pgtype.Text{String: referenceType, Valid: true},
	})
}

func (s *Service) GetTransactionCount(ctx context.Context, userID pgtype.UUID) (int64, error) {
	return s.Q.GetLoyaltyTransactionCount(ctx, userID)
}

func (s *Service) GetActiveRewards(ctx context.Context) ([]dbgen.LoyaltyReward, error) {
	return s.Q.GetActiveRewards(ctx)
}

func (s *Service) UpdateProfilePoints(ctx context.Context, userID pgtype.UUID, points, lifetimePoints int32) (dbgen.UpdateLoyaltyProfilePointsRow, error) {
	return s.Q.UpdateLoyaltyProfilePoints(ctx, dbgen.UpdateLoyaltyProfilePointsParams{
		UserID:         userID,
		Points:         points,
		LifetimePoints: lifetimePoints,
	})
}
