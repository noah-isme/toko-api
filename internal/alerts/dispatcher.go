package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/money"
	"github.com/noah-isme/backend-toko/internal/notifications"
)

// Notification types written to the notifications table.
const (
	KindBackInStock = "back_in_stock"
	KindPriceDrop   = "price_drop"
)

const (
	defaultBatchSize        = 100
	defaultMaxSubscriptions = 200
	defaultCurrency         = "IDR"
	defaultRetention        = 7 * 24 * time.Hour
	defaultPruneEvery       = time.Hour
	pruneBatch              = 1000
)

// TxBeginner starts a database transaction. *pgxpool.Pool satisfies it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Dispatcher drains the product_alert_events outbox.
//
// Delivery model: inside ONE transaction a Flush claims subscriptions
// (notified_at), writes the in-app Notification row, the domain_events row and
// a product_alert_deliveries row for each hit, and marks the outbox events
// processed. Email is NOT sent here: the Mailer drains
// product_alert_deliveries asynchronously with retries, so how many
// subscriptions a Flush claims never depends on SMTP latency, and a committed
// alert's email survives restarts and relay outages.
//
// Fairness: pending events are read in (last_attempt_at NULLS FIRST, id)
// order and each event in a pass may claim at most its share of the budget
// (ceil(MaxSubscriptions / events in the batch)). An event with more claimable
// subscriptions than its share stays pending and gets last_attempt_at = now(),
// which moves it behind every event that has not been attempted yet, so one
// popular product (in any tenant) cannot starve the others.
type Dispatcher struct {
	Pool TxBeginner
	Q    *dbgen.Queries
	// Mailer, when set, is kicked after a Flush commits queued emails.
	Mailer Kicker
	// OnError receives non-fatal errors (retention pruning failures).
	OnError func(error)
	// BatchSize caps outbox events per Flush (default 100).
	BatchSize int
	// MaxSubscriptions caps subscription claims per Flush (default 200).
	MaxSubscriptions int
	// Currency formats prices in notification copy (default IDR). Product
	// prices are whole currency units.
	Currency string
	// Retention is how long processed outbox events are kept (default 7d).
	// A negative value disables pruning.
	Retention time.Duration
	// Now is used for pruning; defaults to time.Now.
	Now func() time.Time

	pruneMu   sync.Mutex
	lastPrune time.Time
}

// Result summarizes one Flush (Dispatcher) or one delivery pass (Mailer).
type Result struct {
	// Events is the number of outbox events marked processed.
	Events int
	// Claimed is the number of subscriptions consumed (including opted-out).
	Claimed int
	// Notified is the number of in-app notifications written.
	Notified int
	// Queued is the number of emails queued in product_alert_deliveries.
	Queued int
	// Attempted is the number of email deliveries the Mailer tried.
	Attempted int
	// Delivered is the number of email deliveries that succeeded.
	Delivered int
	// More reports that pending work remains (budget or batch exhausted).
	More bool
}

// Progress reports whether the pass changed any state.
func (r Result) Progress() bool { return r.Events > 0 || r.Claimed > 0 || r.Attempted > 0 }

// hit is one claimable subscription row, normalized across alert types.
type hit struct {
	SubscriptionID pgtype.UUID
	TenantID       pgtype.UUID
	UserID         pgtype.UUID
	ProductID      pgtype.UUID
	VariantID      pgtype.UUID
	Email          string
	ProductTitle   string
	ProductSlug    string
	Price          int64
	PreviousPrice  int64
}

// message is the in-app notification plus the bus payload for one hit.
type message struct {
	Topic   string
	Kind    string
	Title   string
	Body    string
	Data    []byte
	Payload []byte
}

func (d *Dispatcher) batchSize() int {
	if d.BatchSize > 0 {
		return d.BatchSize
	}
	return defaultBatchSize
}

func (d *Dispatcher) maxSubscriptions() int {
	if d.MaxSubscriptions > 0 {
		return d.MaxSubscriptions
	}
	return defaultMaxSubscriptions
}

func (d *Dispatcher) currency() string {
	if d.Currency != "" {
		return d.Currency
	}
	return defaultCurrency
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// perEventShare splits the claim budget evenly across the pending events of
// a pass (rounded up, at least 1).
func perEventShare(budget, events int) int {
	if events <= 0 || budget <= 0 {
		return budget
	}
	share := (budget + events - 1) / events
	if share < 1 {
		share = 1
	}
	return share
}

func (d *Dispatcher) reportError(err error) {
	if err != nil && d.OnError != nil {
		d.OnError(err)
	}
}

// pruneIfDue deletes one bounded batch of old processed outbox events at most
// once per hour per process. Failures are reported, never fatal.
func (d *Dispatcher) pruneIfDue(ctx context.Context) {
	retention := d.Retention
	if retention < 0 {
		return
	}
	if retention == 0 {
		retention = defaultRetention
	}
	now := d.now()
	d.pruneMu.Lock()
	if !d.lastPrune.IsZero() && now.Sub(d.lastPrune) < defaultPruneEvery {
		d.pruneMu.Unlock()
		return
	}
	d.lastPrune = now
	d.pruneMu.Unlock()
	if _, err := d.Q.PruneProcessedAlertEvents(ctx, dbgen.PruneProcessedAlertEventsParams{
		Cutoff:  pgtype.Timestamptz{Time: now.Add(-retention), Valid: true},
		MaxRows: pruneBatch,
	}); err != nil {
		d.reportError(fmt.Errorf("alerts: prune processed events: %w", err))
	}
}

// Flush processes one batch of outbox events. It is nil-safe.
func (d *Dispatcher) Flush(ctx context.Context) (Result, error) {
	if d == nil || d.Pool == nil || d.Q == nil {
		return Result{}, nil
	}
	d.pruneIfDue(ctx)

	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("alerts: begin flush: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	qtx := d.Q.WithTx(tx)

	batch := d.batchSize()
	pending, err := qtx.ListPendingAlertEvents(ctx, int32(batch))
	if err != nil {
		return Result{}, fmt.Errorf("alerts: list pending events: %w", err)
	}

	budget := d.maxSubscriptions()
	share := perEventShare(budget, len(pending))
	var res Result
	processed := make([]int64, 0, len(pending))
	rotated := make([]int64, 0)
	notifier := &notifications.Service{Q: qtx}

	for _, ev := range pending {
		if budget == 0 {
			// Not attempted this pass: keeps its place (last_attempt_at
			// unchanged) for the next pass.
			res.More = true
			continue
		}
		limit := share
		if limit > budget {
			limit = budget
		}
		var hits []hit
		switch ev.AlertType {
		case TypeBackInStock:
			rows, err := qtx.ListPendingBackInStockSubscriptions(ctx, dbgen.ListPendingBackInStockSubscriptionsParams{
				TenantID:  ev.TenantID,
				ProductID: ev.ProductID,
				MaxRows:   int32(limit + 1),
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: list back-in-stock subscriptions: %w", err)
			}
			hits = backInStockHits(rows)
		case TypePriceDrop:
			rows, err := qtx.ListPendingPriceDropSubscriptions(ctx, dbgen.ListPendingPriceDropSubscriptionsParams{
				TenantID:  ev.TenantID,
				ProductID: ev.ProductID,
				MaxRows:   int32(limit + 1),
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: list price-drop subscriptions: %w", err)
			}
			hits = priceDropHits(rows)
		}

		selected, overflow := selectHits(hits, limit)
		for _, h := range selected {
			n, err := qtx.MarkAlertSubscriptionNotified(ctx, dbgen.MarkAlertSubscriptionNotifiedParams{
				ID:       h.SubscriptionID,
				TenantID: h.TenantID,
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: claim subscription: %w", err)
			}
			if n == 0 {
				continue // claimed by another flush
			}
			budget--
			res.Claimed++

			optedIn, err := wantsStockUpdates(ctx, qtx, h.UserID)
			if err != nil {
				return Result{}, err
			}
			if !optedIn {
				// Consumed but not delivered (Decision 4): the user can re-arm.
				continue
			}
			msg, err := buildMessage(ev.AlertType, h, d.currency())
			if err != nil {
				return Result{}, err
			}
			// The domain event, the in-app notification and the email
			// delivery row commit atomically with notified_at; the Mailer
			// sends the email later.
			row, err := qtx.InsertDomainEvent(ctx, dbgen.InsertDomainEventParams{
				Topic:       msg.Topic,
				AggregateID: h.SubscriptionID,
				Payload:     msg.Payload,
				TenantID:    h.TenantID,
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: persist domain event: %w", err)
			}
			// Tied to the event, so the (event, user) unique index keeps the
			// in-app row single.
			if _, err := notifier.Create(ctx, h.UserID, h.TenantID, row.ID, msg.Kind, msg.Title, msg.Body, msg.Data); err != nil {
				return Result{}, fmt.Errorf("alerts: create notification: %w", err)
			}
			res.Notified++
			if err := qtx.InsertAlertDelivery(ctx, dbgen.InsertAlertDeliveryParams{
				EventID:  row.ID,
				TenantID: h.TenantID,
			}); err != nil {
				return Result{}, fmt.Errorf("alerts: queue email delivery: %w", err)
			}
			res.Queued++
		}

		if overflow {
			// Claimable subscriptions remain: keep the event pending and
			// rotate it behind events that have not been attempted yet.
			rotated = append(rotated, ev.ID)
			res.More = true
		} else {
			processed = append(processed, ev.ID)
		}
	}
	if len(pending) == batch {
		res.More = true
	}

	if len(processed) > 0 {
		if err := qtx.MarkAlertEventsProcessed(ctx, processed); err != nil {
			return Result{}, fmt.Errorf("alerts: mark events processed: %w", err)
		}
	}
	if len(rotated) > 0 {
		if err := qtx.MarkAlertEventsAttempted(ctx, rotated); err != nil {
			return Result{}, fmt.Errorf("alerts: rotate events: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("alerts: commit flush: %w", err)
	}
	res.Events = len(processed)
	if res.Queued > 0 && d.Mailer != nil {
		d.Mailer.Kick()
	}
	return res, nil
}

// selectHits truncates hits to budget and reports whether any were left over.
func selectHits(hits []hit, budget int) ([]hit, bool) {
	if budget < 0 {
		budget = 0
	}
	if len(hits) > budget {
		return hits[:budget], true
	}
	return hits, false
}

func backInStockHits(rows []dbgen.ListPendingBackInStockSubscriptionsRow) []hit {
	out := make([]hit, 0, len(rows))
	for _, r := range rows {
		out = append(out, hit{
			SubscriptionID: r.ID,
			TenantID:       r.TenantID,
			UserID:         r.UserID,
			ProductID:      r.ProductID,
			VariantID:      r.VariantID,
			Email:          r.UserEmail,
			ProductTitle:   r.ProductTitle,
			ProductSlug:    r.ProductSlug,
			Price:          r.CurrentPrice,
		})
	}
	return out
}

func priceDropHits(rows []dbgen.ListPendingPriceDropSubscriptionsRow) []hit {
	out := make([]hit, 0, len(rows))
	for _, r := range rows {
		out = append(out, hit{
			SubscriptionID: r.ID,
			TenantID:       r.TenantID,
			UserID:         r.UserID,
			ProductID:      r.ProductID,
			VariantID:      r.VariantID,
			Email:          r.UserEmail,
			ProductTitle:   r.ProductTitle,
			ProductSlug:    r.ProductSlug,
			Price:          r.CurrentPrice,
			PreviousPrice:  r.PriceAtSubscribe.Int64,
		})
	}
	return out
}

// prefsStore is the preference lookup used during a Flush.
type prefsStore interface {
	GetPushPreferences(ctx context.Context, userID pgtype.UUID) (dbgen.PushPreference, error)
}

// wantsStockUpdates applies Decision 4: a missing preferences row means opted
// in; otherwise both the master switch and stock_updates must be on. To keep
// in-app rows for opted-out users, change this gate only.
func wantsStockUpdates(ctx context.Context, q prefsStore, userID pgtype.UUID) (bool, error) {
	prefs, err := q.GetPushPreferences(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, fmt.Errorf("alerts: load preferences: %w", err)
	}
	return optedIn(prefs), nil
}

func optedIn(p dbgen.PushPreference) bool {
	return p.Enabled && p.StockUpdates
}

// buildMessage produces the in-app copy, notification data and bus payload.
func buildMessage(alertType string, h hit, currency string) (message, error) {
	var m message
	data := map[string]any{
		"productId":      uuidString(h.ProductID),
		"variantId":      nullableUUID(h.VariantID),
		"slug":           h.ProductSlug,
		"title":          h.ProductTitle,
		"price":          h.Price,
		"subscriptionId": uuidString(h.SubscriptionID),
	}
	switch alertType {
	case TypeBackInStock:
		m.Topic = events.TopicAlertBackInStock
		m.Kind = KindBackInStock
		m.Title = "Stok kembali tersedia"
		m.Body = fmt.Sprintf("%s sudah tersedia kembali.", h.ProductTitle)
	case TypePriceDrop:
		m.Topic = events.TopicAlertPriceDrop
		m.Kind = KindPriceDrop
		m.Title = "Harga turun"
		m.Body = fmt.Sprintf("%s turun dari %s menjadi %s.", h.ProductTitle,
			money.Format(currency, h.PreviousPrice), money.Format(currency, h.Price))
		data["previousPrice"] = h.PreviousPrice
	default:
		return message{}, fmt.Errorf("alerts: unknown alert type %q", alertType)
	}
	data["topic"] = m.Topic

	payload := map[string]any{
		"subscriptionId": uuidString(h.SubscriptionID),
		"userId":         uuidString(h.UserID),
		"email":          h.Email,
		"tenantId":       uuidString(h.TenantID),
		"productId":      uuidString(h.ProductID),
		"variantId":      nullableUUID(h.VariantID),
		"productSlug":    h.ProductSlug,
		"productTitle":   h.ProductTitle,
		"price":          h.Price,
		"message":        m.Body,
	}
	if alertType == TypePriceDrop {
		payload["previousPrice"] = h.PreviousPrice
	}
	var err error
	if m.Data, err = json.Marshal(data); err != nil {
		return message{}, fmt.Errorf("alerts: encode notification data: %w", err)
	}
	if m.Payload, err = json.Marshal(payload); err != nil {
		return message{}, fmt.Errorf("alerts: encode event payload: %w", err)
	}
	return m, nil
}

func nullableUUID(id pgtype.UUID) any {
	if !id.Valid {
		return nil
	}
	return uuidString(id)
}
