package push

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Store is the subset of dbgen.Querier the push module uses. Satisfied by
// *dbgen.Queries.
type Store interface {
	GetPushSubscription(ctx context.Context, arg dbgen.GetPushSubscriptionParams) (dbgen.PushSubscription, error)
	GetPushSubscriptionsByUser(ctx context.Context, userID pgtype.UUID) ([]dbgen.PushSubscription, error)
	CreatePushSubscription(ctx context.Context, arg dbgen.CreatePushSubscriptionParams) (dbgen.PushSubscription, error)
	DeletePushSubscription(ctx context.Context, arg dbgen.DeletePushSubscriptionParams) error
	DeleteAllPushSubscriptions(ctx context.Context, userID pgtype.UUID) error
	GetPushPreferences(ctx context.Context, userID pgtype.UUID) (dbgen.PushPreference, error)
	UpsertPushPreferences(ctx context.Context, arg dbgen.UpsertPushPreferencesParams) (dbgen.PushPreference, error)
	TrimPushSubscriptions(ctx context.Context, arg dbgen.TrimPushSubscriptionsParams) error
}

// Push preference categories (push_preferences columns).
const (
	CategoryOrderUpdate  = "order_update"
	CategoryPromoUpdates = "promo_updates"
	CategoryStockUpdates = "stock_updates"
)

// Service manages push subscriptions and preferences and sends pushes.
type Service struct {
	Q Store
	// Sender is nil when VAPID keys are not configured (push disabled).
	Sender Sender
	// PublicKey is the VAPID public key handed to browsers.
	PublicKey string
	// AllowedHosts is the push-service host allowlist for new subscriptions.
	AllowedHosts []string
	// MaxSubscriptions caps stored subscriptions per user (oldest evicted).
	MaxSubscriptions int32
}

// SendResult counts the outcome of a SendToUser call.
type SendResult struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
	Pruned int `json:"pruned"`
}

// Enabled reports whether web push is configured.
func (s *Service) Enabled() bool {
	return s != nil && s.Sender != nil && s.PublicKey != ""
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

// CreateSubscription validates and stores (upserts) a subscription, then evicts
// the user's oldest subscriptions beyond MaxSubscriptions.
func (s *Service) CreateSubscription(ctx context.Context, userID pgtype.UUID, endpoint, p256dh, auth string) (dbgen.PushSubscription, error) {
	if err := ValidateEndpoint(endpoint, s.AllowedHosts); err != nil {
		return dbgen.PushSubscription{}, err
	}
	sub, err := s.Q.CreatePushSubscription(ctx, dbgen.CreatePushSubscriptionParams{
		UserID:   userID,
		Endpoint: endpoint,
		P256dh:   p256dh,
		Auth:     auth,
	})
	if err != nil {
		return dbgen.PushSubscription{}, err
	}
	keep := s.MaxSubscriptions
	if keep <= 0 {
		keep = 10
	}
	if err := s.Q.TrimPushSubscriptions(ctx, dbgen.TrimPushSubscriptionsParams{UserID: userID, Offset: keep}); err != nil {
		return dbgen.PushSubscription{}, fmt.Errorf("trim subscriptions: %w", err)
	}
	return sub, nil
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

// Allowed reports whether the user accepts pushes of a category. Users without
// stored preferences get the defaults (everything on).
func (s *Service) Allowed(ctx context.Context, userID pgtype.UUID, category string) (bool, error) {
	prefs, err := s.Q.GetPushPreferences(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	if !prefs.Enabled {
		return false, nil
	}
	switch category {
	case CategoryOrderUpdate:
		return prefs.OrderUpdates, nil
	case CategoryPromoUpdates:
		return prefs.PromoUpdates, nil
	case CategoryStockUpdates:
		return prefs.StockUpdates, nil
	default:
		return true, nil
	}
}

// SendToUser sends msg to every stored subscription of the user.
//
//   - 2xx: counted as sent.
//   - 404/410 from the push service, a non-https endpoint, undecodable keys or
//     an endpoint resolving to a non-public address: the subscription is
//     deleted (pruned) and no error is returned for it.
//   - 429, 5xx and network errors: counted as failed and returned (joined), so
//     a queue job retries.
//   - Other 4xx (bad VAPID config, payload too large, ...): counted as failed
//     and logged; not returned, since a retry cannot succeed.
//
// Returned errors and log lines name the push service host, never the
// endpoint URL (its path is the subscription's bearer token).
func (s *Service) SendToUser(ctx context.Context, userID pgtype.UUID, msg Message) (SendResult, error) {
	var result SendResult
	if !s.Enabled() {
		return result, ErrPushDisabled
	}
	subs, err := s.Q.GetPushSubscriptionsByUser(ctx, userID)
	if err != nil {
		return result, fmt.Errorf("push: load subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return result, ErrNoSubscriptions
	}
	logger := zerolog.Ctx(ctx)
	var retryable error
	for _, sub := range subs {
		host := endpointHost(sub.Endpoint)
		subLog := logger.With().Str("subscription_id", uuidString(sub.ID)).Str("push_host", host).Logger()
		if err := SendableEndpoint(sub.Endpoint); err != nil {
			s.prune(ctx, &result, sub, &subLog, "endpoint is not https")
			continue
		}
		status, err := s.Sender.Send(ctx, sub, msg)
		if err != nil {
			var de *DeliveryError
			if errors.As(err, &de) && de.Permanent {
				s.prune(ctx, &result, sub, &subLog, de.Class)
				continue
			}
			result.Failed++
			if de == nil {
				// Not from WebPushSender: keep only the type, not the text.
				de = &DeliveryError{Host: host, Class: fmt.Sprintf("send error (%T)", err)}
			}
			subLog.Warn().Str("class", de.Class).Msg("push send failed")
			retryable = errors.Join(retryable, de)
			continue
		}
		switch {
		case status >= 200 && status < 300:
			result.Sent++
		case status == http.StatusNotFound || status == http.StatusGone:
			s.prune(ctx, &result, sub, &subLog, fmt.Sprintf("push service returned %d", status))
		case status == http.StatusTooManyRequests || status >= 500:
			result.Failed++
			subLog.Warn().Int("status", status).Msg("push service temporarily rejected the message")
			retryable = errors.Join(retryable, fmt.Errorf("push: status %d from %s", status, host))
		default:
			result.Failed++
			subLog.Warn().Int("status", status).Msg("push service rejected the message; not retrying")
		}
	}
	return result, retryable
}

func (s *Service) prune(ctx context.Context, result *SendResult, sub dbgen.PushSubscription, logger *zerolog.Logger, reason string) {
	if err := s.Q.DeletePushSubscription(ctx, dbgen.DeletePushSubscriptionParams{UserID: sub.UserID, Endpoint: sub.Endpoint}); err != nil {
		result.Failed++
		logger.Error().Err(err).Str("reason", reason).Msg("prune push subscription failed")
		return
	}
	result.Pruned++
	logger.Info().Str("reason", reason).Msg("pruned push subscription")
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
