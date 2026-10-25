package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// defaultEmitTimeout is the worst-case cost of delivering one alert after
	// commit. The email notifier sends synchronously through SMTPSender, whose
	// dial and conversation each have a 10s bound, so 20s covers one message.
	defaultEmitTimeout = 20 * time.Second
	defaultCurrency    = "IDR"
)

// EventDeliverer fans an already-persisted domain event out to notifiers.
// *events.Bus satisfies it (Bus.Deliver).
type EventDeliverer interface {
	Deliver(ctx context.Context, ev dbgen.DomainEvent) error
}

// TxBeginner starts a database transaction. *pgxpool.Pool satisfies it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Dispatcher drains the product_alert_events outbox.
//
// Delivery model: inside ONE transaction a Flush claims subscriptions
// (notified_at), writes the in-app Notification row AND the domain_events row
// for each hit, and marks the outbox events processed. Only after commit are
// the persisted domain events handed to the notifiers (email), each with its
// own timeout detached from the caller's deadline, so a run deadline can never
// drop a committed alert's email. The number of claims per Flush is capped so
// that, even at EmitTimeout per delivery, the deliveries fit in the time left
// on the caller's context.
type Dispatcher struct {
	Pool   TxBeginner
	Q      *dbgen.Queries
	Events EventDeliverer
	// OnError receives non-fatal errors (delivery failures).
	OnError func(error)
	// BatchSize caps outbox events per Flush (default 100).
	BatchSize int
	// MaxSubscriptions caps subscription claims per Flush (default 200).
	MaxSubscriptions int
	// EmitTimeout is the per-delivery timeout and the worst-case cost used to
	// size the claim budget (default 20s).
	EmitTimeout time.Duration
	// Currency formats prices in notification copy (default IDR). Product
	// prices are whole currency units.
	Currency string
	// Now is used for the claim budget; defaults to time.Now.
	Now func() time.Time
}

// Result summarizes one Flush.
type Result struct {
	// Events is the number of outbox events marked processed.
	Events int
	// Claimed is the number of subscriptions consumed (including opted-out).
	Claimed int
	// Notified is the number of in-app notifications written.
	Notified int
	// Delivered is the number of post-commit deliveries that succeeded.
	Delivered int
	// More reports that pending work remains (budget or batch exhausted).
	More bool
}

// Progress reports whether the Flush changed any state.
func (r Result) Progress() bool { return r.Events > 0 || r.Claimed > 0 }

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

func (d *Dispatcher) emitTimeout() time.Duration {
	if d.EmitTimeout > 0 {
		return d.EmitTimeout
	}
	return defaultEmitTimeout
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

// claimBudget returns how many subscriptions this Flush may claim: at most
// MaxSubscriptions and, when ctx has a deadline, no more than the number of
// worst-case deliveries that fit in the remaining time.
func (d *Dispatcher) claimBudget(ctx context.Context) int {
	budget := d.MaxSubscriptions
	if budget <= 0 {
		budget = defaultMaxSubscriptions
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := deadline.Sub(d.now())
		byTime := int(remaining / d.emitTimeout())
		if byTime < budget {
			budget = byTime
		}
	}
	if budget < 0 {
		budget = 0
	}
	return budget
}

func (d *Dispatcher) reportError(err error) {
	if err != nil && d.OnError != nil {
		d.OnError(err)
	}
}

// Flush processes one batch of outbox events. It is nil-safe.
func (d *Dispatcher) Flush(ctx context.Context) (Result, error) {
	if d == nil || d.Pool == nil || d.Q == nil {
		return Result{}, nil
	}
	budget := d.claimBudget(ctx)
	if budget == 0 {
		// Not enough time left to deliver even one alert; claim nothing.
		return Result{More: true}, nil
	}

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

	var res Result
	processed := make([]int64, 0, len(pending))
	queued := make([]dbgen.DomainEvent, 0)
	notifier := &notifications.Service{Q: qtx}

	for i, ev := range pending {
		var hits []hit
		switch ev.AlertType {
		case TypeBackInStock:
			rows, err := qtx.ListPendingBackInStockSubscriptions(ctx, dbgen.ListPendingBackInStockSubscriptionsParams{
				TenantID:  ev.TenantID,
				ProductID: ev.ProductID,
				MaxRows:   int32(budget + 1),
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: list back-in-stock subscriptions: %w", err)
			}
			hits = backInStockHits(rows)
		case TypePriceDrop:
			rows, err := qtx.ListPendingPriceDropSubscriptions(ctx, dbgen.ListPendingPriceDropSubscriptionsParams{
				TenantID:  ev.TenantID,
				ProductID: ev.ProductID,
				MaxRows:   int32(budget + 1),
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: list price-drop subscriptions: %w", err)
			}
			hits = priceDropHits(rows)
		}

		selected, overflow := selectHits(hits, budget)
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
			if _, err := notifier.Create(ctx, h.UserID, h.TenantID, msg.Kind, msg.Title, msg.Body, msg.Data); err != nil {
				return Result{}, fmt.Errorf("alerts: create notification: %w", err)
			}
			res.Notified++
			// Persist the domain event in the same transaction so the record
			// commits atomically with notified_at.
			row, err := qtx.InsertDomainEvent(ctx, dbgen.InsertDomainEventParams{
				Topic:       msg.Topic,
				AggregateID: h.SubscriptionID,
				Payload:     msg.Payload,
			})
			if err != nil {
				return Result{}, fmt.Errorf("alerts: persist domain event: %w", err)
			}
			queued = append(queued, dbgen.DomainEvent{
				ID:          row.ID,
				Topic:       row.Topic,
				AggregateID: row.AggregateID,
				Payload:     row.Payload,
				OccurredAt:  row.OccurredAt,
			})
		}

		if overflow {
			// Claimable subscriptions remain for this event; keep it pending.
			res.More = true
		} else {
			processed = append(processed, ev.ID)
		}
		if budget == 0 {
			if overflow || i < len(pending)-1 {
				res.More = true
			}
			break
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
	if err := tx.Commit(ctx); err != nil {
		return Result{}, fmt.Errorf("alerts: commit flush: %w", err)
	}
	res.Events = len(processed)
	res.Delivered = d.deliver(ctx, queued)
	return res, nil
}

// deliver hands committed events to the notifiers. Each delivery gets its own
// timeout derived from a context that ignores the caller's deadline and
// cancellation: the claim budget already sized the batch to the remaining run
// time, and dropping an email whose subscription is committed as notified
// would lose it for good.
func (d *Dispatcher) deliver(ctx context.Context, queued []dbgen.DomainEvent) int {
	if d.Events == nil || len(queued) == 0 {
		return 0
	}
	base := context.WithoutCancel(ctx)
	delivered := 0
	for _, ev := range queued {
		emitCtx, cancel := context.WithTimeout(base, d.emitTimeout())
		err := d.Events.Deliver(emitCtx, ev)
		cancel()
		if err != nil {
			d.reportError(fmt.Errorf("alerts: deliver %s for subscription %s: %w", ev.Topic, uuidString(ev.AggregateID), err))
			continue
		}
		delivered++
	}
	return delivered
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
