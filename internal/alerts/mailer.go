package alerts

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	defaultMailBatch       = 20
	defaultMailConcurrency = 4
	// defaultSendTimeout bounds one delivery. The production sender
	// (SMTPSender) ignores ctx and is bounded by its own dial timeout plus one
	// conversation deadline, each SMTP_TIMEOUT_SEC; cmd/api therefore sets
	// SendTimeout to 2*SMTPTimeout plus slack. This default matches the 10s
	// SMTP default.
	defaultSendTimeout = 25 * time.Second
	defaultMaxAttempts = 6
	markTimeout        = 5 * time.Second
	maxErrorLength     = 500
)

// EventDeliverer fans an already-persisted domain event out to notifiers.
// *events.Bus satisfies it (Bus.Deliver).
type EventDeliverer interface {
	Deliver(ctx context.Context, ev dbgen.DomainEvent) error
}

// mailStore is the persistence surface of the Mailer; *dbgen.Queries
// satisfies it.
type mailStore interface {
	LeaseDueAlertDeliveries(ctx context.Context, arg dbgen.LeaseDueAlertDeliveriesParams) ([]dbgen.LeaseDueAlertDeliveriesRow, error)
	MarkAlertDeliveryDelivered(ctx context.Context, arg dbgen.MarkAlertDeliveryDeliveredParams) error
	MarkAlertDeliveryFailed(ctx context.Context, arg dbgen.MarkAlertDeliveryFailedParams) error
	PruneFinishedAlertDeliveries(ctx context.Context, arg dbgen.PruneFinishedAlertDeliveriesParams) (int64, error)
}

// Mailer is the bounded asynchronous email sender for alerts. It drains
// product_alert_deliveries (written by the Dispatcher in the claim
// transaction) and hands each persisted domain event to the existing
// notifiers through EventDeliverer; it does not send mail itself.
//
// Each pass leases at most BatchSize due rows (next_attempt_at pushed past the
// worst-case time of the pass), sends them with at most Concurrency in flight,
// and records success, a retry with exponential backoff, or gives up after
// MaxAttempts. A pass that is cut short (crash, shutdown) leaves its rows
// leased; they become due again when the lease expires. Delivery is therefore
// at-least-once: a send that outlives its lease, or a crash between a
// successful send and its bookkeeping, can repeat an email.
type Mailer struct {
	Q      mailStore
	Events EventDeliverer
	// OnError receives per-delivery and bookkeeping errors.
	OnError func(error)
	// BatchSize caps rows leased per pass (default 20).
	BatchSize int
	// Concurrency caps sends in flight (default 4).
	Concurrency int
	// SendTimeout bounds one delivery and sizes the lease (default 25s).
	SendTimeout time.Duration
	// MaxAttempts before a row is marked failed (default 6).
	MaxAttempts int
	// Backoff returns the delay before retry n (1-based). Default: 1m * 2^(n-1),
	// capped at 1h.
	Backoff func(attempt int) time.Duration
	// Retention keeps delivered/failed rows this long (default 7d); negative
	// disables pruning.
	Retention time.Duration
	// Now defaults to time.Now.
	Now func() time.Time

	pruneMu   sync.Mutex
	lastPrune time.Time
}

func (m *Mailer) batchSize() int {
	if m.BatchSize > 0 {
		return m.BatchSize
	}
	return defaultMailBatch
}

func (m *Mailer) concurrency() int {
	if m.Concurrency > 0 {
		return m.Concurrency
	}
	return defaultMailConcurrency
}

func (m *Mailer) sendTimeout() time.Duration {
	if m.SendTimeout > 0 {
		return m.SendTimeout
	}
	return defaultSendTimeout
}

func (m *Mailer) maxAttempts() int {
	if m.MaxAttempts > 0 {
		return m.MaxAttempts
	}
	return defaultMaxAttempts
}

func (m *Mailer) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Mailer) backoff(attempt int) time.Duration {
	if m.Backoff != nil {
		return m.Backoff(attempt)
	}
	return defaultBackoff(attempt)
}

func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Minute
	for i := 1; i < attempt && d < time.Hour; i++ {
		d *= 2
	}
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

// leaseDuration is the worst-case wall time of one pass plus one send of
// slack: rows wait for a worker slot, then take at most SendTimeout each.
func (m *Mailer) leaseDuration(rows int) time.Duration {
	waves := (rows + m.concurrency() - 1) / m.concurrency()
	return time.Duration(waves+1) * m.sendTimeout()
}

func (m *Mailer) reportError(err error) {
	if err != nil && m.OnError != nil {
		m.OnError(err)
	}
}

func (m *Mailer) pruneIfDue(ctx context.Context) {
	retention := m.Retention
	if retention < 0 {
		return
	}
	if retention == 0 {
		retention = defaultRetention
	}
	now := m.now()
	m.pruneMu.Lock()
	if !m.lastPrune.IsZero() && now.Sub(m.lastPrune) < defaultPruneEvery {
		m.pruneMu.Unlock()
		return
	}
	m.lastPrune = now
	m.pruneMu.Unlock()
	if _, err := m.Q.PruneFinishedAlertDeliveries(ctx, dbgen.PruneFinishedAlertDeliveriesParams{
		Cutoff:  pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
		MaxRows: pruneBatch,
	}); err != nil {
		m.reportError(fmt.Errorf("alerts: prune deliveries: %w", err))
	}
}

// Flush runs one delivery pass. It is nil-safe and satisfies Flusher, so a
// Runner can drive it.
func (m *Mailer) Flush(ctx context.Context) (Result, error) {
	if m == nil || m.Q == nil || m.Events == nil {
		return Result{}, nil
	}
	m.pruneIfDue(ctx)
	batch := m.batchSize()
	rows, err := m.Q.LeaseDueAlertDeliveries(ctx, dbgen.LeaseDueAlertDeliveriesParams{
		LeaseUntil: pgtype.Timestamptz{Time: m.now().Add(m.leaseDuration(batch)), Valid: true},
		MaxRows:    int32(batch),
	})
	if err != nil {
		return Result{}, fmt.Errorf("alerts: lease deliveries: %w", err)
	}
	res := Result{Attempted: len(rows), More: len(rows) == batch}
	if len(rows) == 0 {
		return res, nil
	}

	// Sends and bookkeeping are detached from the caller's deadline: the rows
	// are leased, so finishing them is always better than abandoning them.
	base := context.WithoutCancel(ctx)
	sem := make(chan struct{}, m.concurrency())
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		delivered int
	)
	for _, row := range rows {
		wg.Add(1)
		sem <- struct{}{}
		go func(row dbgen.LeaseDueAlertDeliveriesRow) {
			defer wg.Done()
			defer func() { <-sem }()
			if m.deliverOne(base, row) {
				mu.Lock()
				delivered++
				mu.Unlock()
			}
		}(row)
	}
	wg.Wait()
	res.Delivered = delivered
	return res, nil
}

// deliverOne sends one leased row and records the outcome.
func (m *Mailer) deliverOne(base context.Context, row dbgen.LeaseDueAlertDeliveriesRow) bool {
	sendCtx, cancel := context.WithTimeout(base, m.sendTimeout())
	err := m.Events.Deliver(sendCtx, dbgen.DomainEvent{
		ID:          row.EventID,
		Topic:       row.Topic,
		AggregateID: row.AggregateID,
		Payload:     row.Payload,
		OccurredAt:  row.OccurredAt,
		TenantID:    row.TenantID,
	})
	cancel()

	markCtx, cancelMark := context.WithTimeout(base, markTimeout)
	defer cancelMark()
	if err == nil {
		if markErr := m.Q.MarkAlertDeliveryDelivered(markCtx, dbgen.MarkAlertDeliveryDeliveredParams{
			EventID:  row.EventID,
			TenantID: row.TenantID,
		}); markErr != nil {
			m.reportError(fmt.Errorf("alerts: mark delivery %s delivered: %w", uuidString(row.EventID), markErr))
		}
		return true
	}

	attempts := int(row.Attempts)
	giveUp := attempts >= m.maxAttempts()
	m.reportError(fmt.Errorf("alerts: deliver %s (attempt %d, give up %v): %w", row.Topic, attempts, giveUp, err))
	msg := err.Error()
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength]
	}
	if markErr := m.Q.MarkAlertDeliveryFailed(markCtx, dbgen.MarkAlertDeliveryFailedParams{
		NextAttemptAt: pgtype.Timestamptz{Time: m.now().Add(m.backoff(attempts)), Valid: true},
		LastError:     pgtype.Text{String: msg, Valid: true},
		GiveUp:        giveUp,
		EventID:       row.EventID,
		TenantID:      row.TenantID,
	}); markErr != nil {
		m.reportError(fmt.Errorf("alerts: mark delivery %s failed: %w", uuidString(row.EventID), markErr))
	}
	return false
}
