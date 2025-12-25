package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// EventStore defines the persistence operations required by the event bus.
type EventStore interface {
	InsertDomainEvent(ctx context.Context, arg dbgen.InsertDomainEventParams) (dbgen.InsertDomainEventRow, error)
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
type Bus struct {
	Store     EventStore
	Scheduler DeliveryScheduler
	Notifiers []Notifier
}

// Emit records the event and dispatches it to all configured handlers.
func (b *Bus) Emit(ctx context.Context, topic string, aggregateID pgtype.UUID, payload any) (dbgen.DomainEvent, error) {
	if b == nil || b.Store == nil {
		return dbgen.DomainEvent{}, errors.New("events: store not configured")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return dbgen.DomainEvent{}, errors.New("events: topic is required")
	}
	if !aggregateID.Valid {
		return dbgen.DomainEvent{}, errors.New("events: aggregate id is required")
	}
	encoded, err := encodePayload(payload)
	if err != nil {
		return dbgen.DomainEvent{}, fmt.Errorf("events: encode payload: %w", err)
	}
	row, err := b.Store.InsertDomainEvent(ctx, dbgen.InsertDomainEventParams{
		Topic:       topic,
		AggregateID: aggregateID,
		Payload:     encoded,
	})
	if err != nil {
		return dbgen.DomainEvent{}, fmt.Errorf("events: persist event: %w", err)
	}
	ev := dbgen.DomainEvent{
		ID:          row.ID,
		Topic:       row.Topic,
		AggregateID: row.AggregateID,
		Payload:     row.Payload,
		OccurredAt:  row.OccurredAt,
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
	return ev, joined
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
