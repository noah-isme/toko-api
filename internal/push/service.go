package push

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type Service struct {
	Q *dbgen.Queries
}

func (s *Service) GetSubscription(ctx context.Context, userID pgtype.UUID, endpointStr string) (dbgen.PushSubscription, error) {
	return s.Q.GetPushSubscription(ctx, dbgen.GetPushSubscriptionParams{
		UserID:   userID,
		Endpoint: endpointStr,
	})
}

func (s *Service) GetSubscriptionsByUser(ctx context.Context, userID pgtype.UUID) ([]dbgen.PushSubscription, error) {
	return s.Q.GetPushSubscriptionsByUser(ctx, userID)
}

func (s *Service) CreateSubscription(ctx context.Context, userID pgtype.UUID, endpoint, p256dh, auth string) (dbgen.PushSubscription, error) {
	return s.Q.CreatePushSubscription(ctx, dbgen.CreatePushSubscriptionParams{
		UserID:   userID,
		Endpoint: endpoint,
		P256dh:   p256dh,
		Auth:     auth,
	})
}

func (s *Service) DeleteSubscription(ctx context.Context, userID pgtype.UUID, endpoint string) error {
	return s.Q.DeletePushSubscription(ctx, dbgen.DeletePushSubscriptionParams{
		UserID:   userID,
		Endpoint: endpoint,
	})
}

func (s *Service) DeleteAllSubscriptions(ctx context.Context, userID pgtype.UUID) error {
	return s.Q.DeleteAllPushSubscriptions(ctx, userID)
}

func (s *Service) GetPreferences(ctx context.Context, userID pgtype.UUID) (dbgen.PushPreference, error) {
	return s.Q.GetPushPreferences(ctx, userID)
}

func (s *Service) UpsertPreferences(ctx context.Context, userID pgtype.UUID, enabled, orderUpdates, promoUpdates, stockUpdates bool) (dbgen.PushPreference, error) {
	return s.Q.UpsertPushPreferences(ctx, dbgen.UpsertPushPreferencesParams{
		UserID:       userID,
		Enabled:      enabled,
		OrderUpdates: orderUpdates,
		PromoUpdates: promoUpdates,
		StockUpdates: stockUpdates,
	})
}
