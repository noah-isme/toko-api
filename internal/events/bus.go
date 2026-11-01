package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// ErrPersist marks an Emit failure that happened before the event row was
// stored. Such an event is lost; any other Emit error means the row exists and
// the outbox relay will retry its fan-out.
var ErrPersist = errors.New("events: event not persisted")

// maxDispatchErrorLen bounds what is stored in domain_events.last_dispatch_error.
const maxDispatchErrorLen = 2000

// emitDetachedTimeout bounds EmitLogged, which runs detached from the caller's
// cancellation so a client disconnect cannot drop an event mid-write.
const emitDetachedTimeout = 15 * time.Second

// EventStore defines the persistence operations required by the event bus.
// Satisfied by *dbgen.Queries.
type EventStore interface {
	InsertDomainEvent(ctx context.Context, arg dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error)
	MarkDomainEventDispatched(ctx context.Context, id pgtype.UUID) error
	RecordDomainEventDispatchFailure(ctx context.Context, arg dbgen.RecordDomainEventDispatchFailureParams) error
}

// DeliveryScheduler schedules webhook deliveries for emitted events.
type DeliveryScheduler interface {
	Schedule(ctx context.Context, event dbgen.DomainEvent) error
}

// Notifier reacts to emitted events (e.g. email, metrics, etc.).
type Notifier interface {
	Notify(ctx context.Context, event dbgen.DomainEvent) error
}

// Bus persists domain events and fans them out to downstream handlers.
//
// domain_events doubles as an outbox: a row is inserted with dispatched_at
// NULL, and Dispatch marks it dispatched once every handler accepted it. Rows
// whose fan-out failed (or whose process crashed in between) are picked up
// again by OutboxRelay. Handlers must therefore be idempotent per event id.
type Bus struct {
	Store     EventStore
	Scheduler DeliveryScheduler
	Notifiers []Notifier
}

// Emit records the event and dispatches it to all configured handlers. An error
// wrapping ErrPersist means nothing was stored; any other error means the row
// exists and its fan-out will be retried by the outbox relay.
func (b *Bus) Emit(ctx context.Context, topic string, aggregateID pgtype.UUID, payload any) (dbgen.DomainEvent, error) {
	if b == nil || b.Store == nil {
		return dbgen.DomainEvent{}, fmt.Errorf("%w: store not configured", ErrPersist)
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return dbgen.DomainEvent{}, fmt.Errorf("%w: topic is required", ErrPersist)
	}
	if !aggregateID.Valid {
		return dbgen.DomainEvent{}, fmt.Errorf("%w: aggregate id is required", ErrPersist)
	}
	encoded, err := encodePayload(payload)
	if err != nil {
		return dbgen.DomainEvent{}, fmt.Errorf("%w: encode payload: %v", ErrPersist, err)
	}
	ev, err := b.Store.InsertDomainEvent(ctx, dbgen.InsertDomainEventParams{
		Topic:       topic,
		AggregateID: aggregateID,
		Payload:     encoded,
		TenantID:    tenantFromContext(ctx),
	})
	if err != nil {
		return dbgen.DomainEvent{}, fmt.Errorf("%w: %w", ErrPersist, err)
	}
	return ev, b.Dispatch(ctx, ev)
}

// EmitLogged emits the event and logs, rather than returns, any failure, using
// the logger attached to ctx (zerolog.Ctx). It runs detached from ctx's
// cancellation (bounded by its own timeout): emitters call it after their
// business transaction committed, and a client disconnecting at that moment
// must not cancel the insert or the enqueue halfway.
func (b *Bus) EmitLogged(ctx context.Context, topic string, aggregateID pgtype.UUID, payload any) {
	if b == nil {
		return
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), emitDetachedTimeout)
	defer cancel()
	ev, err := b.Emit(detached, topic, aggregateID, payload)
	if err == nil {
		return
	}
	logger := zerolog.Ctx(ctx)
	if errors.Is(err, ErrPersist) {
		logger.Error().Err(err).
			Str("topic", topic).
			Str("aggregate_id", uuidString(aggregateID)).
			Msg("domain event not persisted; downstream notifications are lost")
		return
	}
	logger.Warn().Err(err).
		Str("topic", topic).
		Str("aggregate_id", uuidString(aggregateID)).
		Str("event_id", uuidString(ev.ID)).
		Msg("domain event fan-out failed; the outbox relay will retry")
}

// Dispatch fans an already-persisted event out to the webhook scheduler and all
// notifiers. On success the row is marked dispatched; on failure the attempt
// is recorded (dispatch_attempts, last_dispatch_error) and the joined error is
// returned, leaving the row for the outbox relay.
func (b *Bus) Dispatch(ctx context.Context, ev dbgen.DomainEvent) error {
	if b == nil || b.Store == nil {
		return errors.New("events: store not configured")
	}
	var joined error
	if b.Scheduler != nil {
		if schedErr := b.Scheduler.Schedule(ctx, ev); schedErr != nil {
			joined = errors.Join(joined, fmt.Errorf("events: schedule deliveries: %w", schedErr))
		}
	}
	for _, notifier := range b.Notifiers {
		if notifier == nil {
			continue
		}
		if notifyErr := notifier.Notify(ctx, ev); notifyErr != nil {
			joined = errors.Join(joined, fmt.Errorf("events: notifier: %w", notifyErr))
		}
	}
	if joined != nil {
		msg := joined.Error()
		if len(msg) > maxDispatchErrorLen {
			msg = msg[:maxDispatchErrorLen]
		}
		if recErr := b.Store.RecordDomainEventDispatchFailure(ctx, dbgen.RecordDomainEventDispatchFailureParams{
			ID:                ev.ID,
			LastDispatchError: pgtype.Text{String: msg, Valid: true},
		}); recErr != nil {
			joined = errors.Join(joined, fmt.Errorf("events: record dispatch failure: %w", recErr))
		}
		return joined
	}
	if err := b.Store.MarkDomainEventDispatched(ctx, ev.ID); err != nil {
		return fmt.Errorf("events: mark dispatched: %w", err)
	}
	return nil
}

func encodePayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte("{}"), nil
	}
	switch v := payload.(type) {
	case []byte:
		if len(v) == 0 {
			return []byte("{}"), nil
		}
		if !json.Valid(v) {
			return nil, errors.New("payload is not valid json")
		}
		return append([]byte(nil), v...), nil
	case json.RawMessage:
		if len(v) == 0 {
			return []byte("{}"), nil
		}
		if !json.Valid(v) {
			return nil, errors.New("payload is not valid json")
		}
		return append([]byte(nil), v...), nil
	case string:
		if strings.TrimSpace(v) == "" {
			return []byte("{}"), nil
		}
		data := []byte(v)
		if !json.Valid(data) {
			return nil, errors.New("payload is not valid json")
		}
		return data, nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
}

// tenantFromContext returns the request tenant as stored on domain_events, or
// an invalid UUID when ctx carries none.
func tenantFromContext(ctx context.Context) pgtype.UUID {
	raw, ok := tenant.FromContext(ctx)
	if !ok {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}
}
