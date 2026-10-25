package events

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// OutboxStore lists events whose fan-out has not completed. Satisfied by
// *dbgen.Queries.
type OutboxStore interface {
	ListUndispatchedDomainEvents(ctx context.Context, arg dbgen.ListUndispatchedDomainEventsParams) ([]dbgen.DomainEvent, error)
}

// RelayLocker limits sweeps across worker replicas. Satisfied by lock.Locker.
// TryLease must not block: a replica whose tick finds the lease taken skips
// that sweep.
type RelayLocker interface {
	TryLease(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

const outboxRelayLockKey = "lock:outbox:relay"

// OutboxRelay re-dispatches domain events whose first fan-out failed or never
// ran (process crash, Redis outage between insert and enqueue). Every handler on
// the bus is idempotent per event id, so a re-dispatch never duplicates in-app
// notifications or webhook deliveries, and email/push tasks are deduplicated
// by the queue.
type OutboxRelay struct {
	Store OutboxStore
	Bus   *Bus
	// Locker is optional; nil runs without cross-replica locking (tests).
	Locker RelayLocker
	// Interval between sweeps (default 15s).
	Interval time.Duration
	// MinAge is the grace period before an undispatched event is considered
	// stuck (default 30s). It keeps the relay from racing the original Emit.
	MinAge time.Duration
	// MaxAge is how far back the relay looks (default 24h). Older events are
	// left alone: re-sending a days-old transactional message helps nobody,
	// and the window keeps each sweep a bounded index range scan.
	MaxAge time.Duration
	// MaxAttempts stops re-dispatching an event after this many failures
	// (default 10).
	MaxAttempts int32
	// BatchSize caps the events handled per sweep (default 100).
	BatchSize int32
	// Topics restricts the sweep (default DefaultTopics()).
	Topics []string
	Logger *zerolog.Logger
}

// Run sweeps every Interval until ctx is cancelled. It returns nil on
// cancellation; sweep errors are logged and do not stop the loop.
func (r *OutboxRelay) Run(ctx context.Context) error {
	if r == nil || r.Store == nil || r.Bus == nil {
		return errors.New("events: outbox relay not configured")
	}
	ticker := time.NewTicker(r.interval())
	defer ticker.Stop()
	logger := r.logger()
	logger.Info().Dur("interval", r.interval()).Dur("min_age", r.minAge()).Msg("outbox relay started")
	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("outbox relay stopped")
			return nil
		case <-ticker.C:
			if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
				logger.Error().Err(err).Msg("outbox relay sweep failed")
			}
		}
	}
}

// RunOnce performs a single sweep and returns how many events were dispatched
// successfully. With a Locker, the sweep runs only if this replica takes the
// relay lease, which then expires on its own (it is not released when the
// sweep ends), so all replicas together sweep about once per Interval rather
// than once per replica per Interval. A busy lease returns (0, nil).
func (r *OutboxRelay) RunOnce(ctx context.Context) (int, error) {
	if r == nil || r.Store == nil || r.Bus == nil {
		return 0, errors.New("events: outbox relay not configured")
	}
	if r.Locker != nil {
		ok, err := r.Locker.TryLease(ctx, outboxRelayLockKey, r.leaseTTL())
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}
	}
	return r.sweep(ctx)
}

func (r *OutboxRelay) sweep(ctx context.Context) (int, error) {
	now := time.Now()
	topics := r.Topics
	if len(topics) == 0 {
		topics = DefaultTopics()
	}
	rows, err := r.Store.ListUndispatchedDomainEvents(ctx, dbgen.ListUndispatchedDomainEventsParams{
		Topics:      topics,
		NotBefore:   pgtype.Timestamptz{Time: now.Add(-r.maxAge()), Valid: true},
		OlderThan:   pgtype.Timestamptz{Time: now.Add(-r.minAge()), Valid: true},
		MaxAttempts: r.maxAttempts(),
		BatchSize:   r.batchSize(),
	})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	logger := r.logger()
	dispatched := 0
	for _, ev := range rows {
		if ctx.Err() != nil {
			break
		}
		if err := r.Bus.Dispatch(ctx, ev); err != nil {
			logger.Warn().Err(err).
				Str("event_id", uuidString(ev.ID)).
				Str("topic", ev.Topic).
				Int32("attempts", ev.DispatchAttempts+1).
				Msg("outbox re-dispatch failed")
			continue
		}
		dispatched++
	}
	logger.Info().Int("found", len(rows)).Int("dispatched", dispatched).Msg("outbox relay sweep")
	return dispatched, nil
}

func (r *OutboxRelay) interval() time.Duration {
	if r.Interval <= 0 {
		return 15 * time.Second
	}
	return r.Interval
}

// leaseTTL is slightly shorter than the interval so the replica that swept
// last can take the lease again on its own next tick despite timer jitter. A
// sweep that outlasts the lease may overlap the next one; handlers are
// idempotent per event, so that only costs duplicate work.
func (r *OutboxRelay) leaseTTL() time.Duration {
	return r.interval() * 9 / 10
}

func (r *OutboxRelay) minAge() time.Duration {
	if r.MinAge <= 0 {
		return 30 * time.Second
	}
	return r.MinAge
}

func (r *OutboxRelay) maxAge() time.Duration {
	if r.MaxAge <= 0 {
		return 24 * time.Hour
	}
	return r.MaxAge
}

func (r *OutboxRelay) maxAttempts() int32 {
	if r.MaxAttempts <= 0 {
		return 10
	}
	return r.MaxAttempts
}

func (r *OutboxRelay) batchSize() int32 {
	if r.BatchSize <= 0 {
		return 100
	}
	return r.BatchSize
}

func (r *OutboxRelay) logger() *zerolog.Logger {
	if r.Logger == nil {
		nop := zerolog.Nop()
		return &nop
	}
	return r.Logger
}
