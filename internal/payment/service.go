package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/noah-isme/backend-toko/internal/cart"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/obs"
)

// Service coordinates payment intents and status retrieval.
type Service struct {
	Q               *dbgen.Queries
	Provider        Provider
	IntentTTL       time.Duration
	CallbackBaseURL string
}

// CreateIntent creates (or reuses) a payment intent for the provided order.
func (s *Service) CreateIntent(ctx context.Context, orderID string, amount int64, channel string, cbBase string) (dbgen.Payment, error) {
	var zero dbgen.Payment
	if s == nil || s.Q == nil || s.Provider == nil {
		return zero, errors.New("payment service not configured")
	}
	ctx, span := otel.Tracer("payment.Service").Start(ctx, "PaymentService.CreateIntent")
	defer span.End()

	start := time.Now()
	providerName := inferProviderName(s.Provider)
	channelLabel := normaliseLabel(channel)
	result := "error"
	defer func() {
		span.SetAttributes(
			attribute.String("payment.provider", providerName),
			attribute.String("payment.channel", channelLabel),
			attribute.Float64("payment.intent.duration_ms", obs.DurationMillis(time.Since(start))),
			attribute.String("payment.intent.result", result),
		)
		if obs.PaymentIntentTotal != nil {
			obs.PaymentIntentTotal.WithLabelValues(providerName, channelLabel, result).Inc()
		}
	}()
	if cbBase == "" {
		cbBase = s.CallbackBaseURL
	}
	ttl := s.IntentTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	orderUUID, err := cart.ToUUID(orderID)
	if err != nil {
		return zero, fmt.Errorf("invalid order id: %w", err)
	}
	span.SetAttributes(attribute.String("order.id", orderID))
	order, err := s.Q.GetOrderByID(ctx, orderUUID)
	if err != nil {
		return zero, err
	}
	if order.Status != dbgen.OrderStatusPENDINGPAYMENT {
		return zero, fmt.Errorf("order status %s does not allow new intents", order.Status)
	}
	expectedAmount := order.PricingTotal
	if amount > 0 && amount != expectedAmount {
		return zero, fmt.Errorf("amount mismatch: got %d expected %d", amount, expectedAmount)
	}

	existing, err := s.Q.GetLatestPaymentByOrder(ctx, orderUUID)
	if err == nil {
		if existing.Status == dbgen.PaymentStatusPAID {
			return zero, errors.New("order already paid")
		}
		if existing.Status == dbgen.PaymentStatusPENDING {
			if !existing.ExpiresAt.Valid || existing.ExpiresAt.Time.After(time.Now()) {
				if existing.Provider.Valid {
					providerName = normaliseLabel(existing.Provider.String)
				}
				result = "reused"
				return dbgen.Payment{
					ID:              existing.ID,
					OrderID:         existing.OrderID,
					Provider:        existing.Provider,
					Status:          existing.Status,
					ProviderPayload: existing.ProviderPayload,
					CreatedAt:       existing.CreatedAt,
					UpdatedAt:       existing.UpdatedAt,
					Channel:         existing.Channel,
					IntentToken:     existing.IntentToken,
					RedirectUrl:     existing.RedirectUrl,
					Amount:          existing.Amount,
					ExpiresAt:       existing.ExpiresAt,
					TenantID:        pgtype.UUID{}, // Not in Row yet?
				}, nil
			}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}

	req := IntentRequest{
		OrderID:         orderID,
		Amount:          expectedAmount,
		Channel:         channel,
		ExpiresAtSec:    int(ttl.Seconds()),
		CallbackBaseURL: cbBase,
	}
	resp, err := s.Provider.CreateIntent(ctx, req)
	if err != nil {
		span.RecordError(err)
		return zero, err
	}
	providerName = resp.Provider
	if providerName == "" {
		providerName = inferProviderName(s.Provider)
	}
	providerName = normaliseLabel(providerName)
	result = "success"
	payload := toJSON(map[string]any{
		"request":  req,
		"response": resp,
	})
	expiresAt := pgtype.Timestamptz{}
	if resp.ExpiresAt > 0 {
		expiresAt.Valid = true
		expiresAt.Time = time.Unix(resp.ExpiresAt, 0)
	} else {
		expiresAt.Valid = true
		expiresAt.Time = time.Now().Add(ttl)
	}
	payment, err := s.Q.CreatePayment(ctx, dbgen.CreatePaymentParams{
		OrderID:         orderUUID,
		Provider:        pgtype.Text{String: providerName, Valid: providerName != ""},
		Channel:         pgtype.Text{String: channel, Valid: strings.TrimSpace(channel) != ""},
		Status:          dbgen.PaymentStatusPENDING,
		ProviderPayload: payload,
		IntentToken:     pgtype.Text{String: resp.Token, Valid: strings.TrimSpace(resp.Token) != ""},
		RedirectUrl:     pgtype.Text{String: resp.RedirectURL, Valid: strings.TrimSpace(resp.RedirectURL) != ""},
		Amount:          pgtype.Int8{Int64: expectedAmount, Valid: true},
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return zero, err
	}
	_ = s.Q.InsertPaymentEvent(ctx, dbgen.InsertPaymentEventParams{
		PaymentID: payment.ID,
		Status:    dbgen.PaymentStatusPENDING,
		Payload:   payload,
	})
	return dbgen.Payment{
		ID:              payment.ID,
		OrderID:         payment.OrderID,
		Provider:        payment.Provider,
		Status:          payment.Status,
		ProviderPayload: payment.ProviderPayload,
		CreatedAt:       payment.CreatedAt,
		UpdatedAt:       payment.UpdatedAt,
		Channel:         payment.Channel,
		IntentToken:     payment.IntentToken,
		RedirectUrl:     payment.RedirectUrl,
		Amount:          payment.Amount,
		ExpiresAt:       payment.ExpiresAt,
	}, nil
}

// ConsolidatedStatus returns the best-known status for an order payment.
func (s *Service) ConsolidatedStatus(ctx context.Context, orderID string) (string, error) {
	if s == nil || s.Q == nil {
		return "", errors.New("payment service not configured")
	}
	orderUUID, err := cart.ToUUID(orderID)
	if err != nil {
		return "", fmt.Errorf("invalid order id: %w", err)
	}
	payment, err := s.Q.GetLatestPaymentByOrder(ctx, orderUUID)
	if err == nil {
		return string(payment.Status), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	ord, err := s.Q.GetOrderByID(ctx, orderUUID)
	if err != nil {
		return "", err
	}
	switch ord.Status {
	case dbgen.OrderStatusPAID:
		return "PAID", nil
	case dbgen.OrderStatusCANCELED:
		return "FAILED", nil
	case dbgen.OrderStatusPENDINGPAYMENT:
		fallthrough
	default:
		return "PENDING", nil
	}
}

func inferProviderName(p Provider) string {
	switch p.(type) {
	case Midtrans:
		return "midtrans"
	case Xendit:
		return "xendit"
	default:
		return ""
	}
}

func normaliseLabel(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

func toJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
