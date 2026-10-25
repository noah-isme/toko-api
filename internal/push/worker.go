package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// EventLoader loads a domain event by id. Satisfied by *dbgen.Queries.
type EventLoader interface {
	GetDomainEvent(ctx context.Context, id pgtype.UUID) (dbgen.DomainEvent, error)
}

// OrderLookup resolves the order behind an event. Satisfied by *dbgen.Queries.
type OrderLookup interface {
	GetOrderByID(ctx context.Context, id pgtype.UUID) (dbgen.Order, error)
}

// Worker handles push-delivery tasks in cmd/worker. Returned errors make the
// queue retry with backoff and finally park the task in queue_dlq; they come
// from SendToUser and name only push-service hosts, never endpoint URLs.
type Worker struct {
	Svc    *Service
	Events EventLoader
	Orders OrderLookup
	Logger *zerolog.Logger
}

// Handle processes one push-delivery task payload.
func (w *Worker) Handle(ctx context.Context, payload []byte) error {
	if w == nil || w.Svc == nil || w.Events == nil || w.Orders == nil {
		return errors.New("push worker: not configured")
	}
	var task Task
	if err := json.Unmarshal(payload, &task); err != nil {
		return fmt.Errorf("push worker: invalid payload: %w", err)
	}
	parsed, err := uuid.Parse(strings.TrimSpace(task.EventID))
	if err != nil {
		return fmt.Errorf("push worker: invalid event id: %w", err)
	}
	logger := w.logger().With().Str("event_id", parsed.String()).Str("topic", task.Topic).Logger()
	if !w.Svc.Enabled() {
		logger.Warn().Msg("push task dropped: web push is not configured on this worker")
		return nil
	}
	event, err := w.Events.GetDomainEvent(ctx, pgtype.UUID{Bytes: parsed, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			logger.Warn().Msg("push task dropped: event not found")
			return nil
		}
		return fmt.Errorf("push worker: load event: %w", err)
	}
	orderID, ok := events.OrderIDFor(event)
	if !ok {
		return nil
	}
	order, err := w.Orders.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("push worker: load order: %w", err)
	}
	if !order.UserID.Valid {
		return nil
	}
	msg, category, ok := BuildMessage(event, order.ID)
	if !ok {
		return nil
	}
	allowed, err := w.Svc.Allowed(ctx, order.UserID, category)
	if err != nil {
		return fmt.Errorf("push worker: load preferences: %w", err)
	}
	if !allowed {
		return nil
	}
	result, err := w.Svc.SendToUser(logger.WithContext(ctx), order.UserID, msg)
	if errors.Is(err, ErrNoSubscriptions) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("push-delivery: %d of %d subscription(s) failed: %w",
			result.Failed, result.Sent+result.Failed+result.Pruned, err)
	}
	return nil
}

func (w *Worker) logger() *zerolog.Logger {
	if w.Logger == nil {
		nop := zerolog.Nop()
		return &nop
	}
	return w.Logger
}
