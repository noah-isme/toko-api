package jobs

// Lock order (shared with the payment webhook, admin payment confirmation,
// customer cancel, payment intent creation and checkout; see
// internal/reservation): orders row -> payments rows -> inventory_reservations
// (ascending variant_id) -> product_variants (ascending variant_id) ->
// flash_sale_order_items / flash_sale_items (ascending id) -> vouchers.
//
// Phase B takes the order row with SKIP LOCKED and works on one order per
// transaction. Phase A never takes an order lock; it only touches reservations
// of orders that are already CANCELLED or EXPIRED, with SKIP LOCKED, then
// variants in ascending id, then flash-sale items in ascending id.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/cart"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/db/txretry"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/reservation"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// CheckoutExpiryJobName is the job's name and lock suffix.
const CheckoutExpiryJobName = "checkout_expiry"

// DefaultLegacyProviderWindow is how long a Midtrans intent created without
// the Snap expiry (before this release) stays payable at Midtrans: its
// default for VA and bank transfer is about 24h.
const DefaultLegacyProviderWindow = 24 * time.Hour

// maxBatchesPerTenant bounds one run so a huge backlog drains over several
// ticks instead of holding the job lock for a long time.
const maxBatchesPerTenant = 50

// CheckoutExpiryJob expires unpaid orders whose payment deadline (plus grace)
// has passed (Phase B) and releases expired reservations left on terminal
// orders (Phase A).
type CheckoutExpiryJob struct {
	Pool   *pgxpool.Pool
	Q      *dbgen.Queries
	Events *events.Bus
	// FallbackTTL is the payment window for orders without a payment row.
	FallbackTTL time.Duration
	// Grace keeps an order payable this long after its payment deadline, to
	// absorb late provider notifications.
	Grace time.Duration
	// SilentAfter: orders whose deadline+grace passed longer ago than this are
	// expired without customer notifications or domain events (backlog).
	// Orders with a payment proof are never expired silently.
	SilentAfter time.Duration
	// LegacyProviderWindow is the deadline floor (from the intent's creation)
	// for PENDING Midtrans intents that were created without the Snap expiry;
	// zero uses DefaultLegacyProviderWindow.
	LegacyProviderWindow time.Duration
	// ProofHold keeps an order with a payment proof pending this long after
	// its first proof, so an admin can confirm the transfer.
	ProofHold time.Duration
	BatchSize int
	Logger    *zerolog.Logger
	Now       func() time.Time
	// Tenants overrides the tenant list (tests); nil lists active tenants.
	Tenants func(ctx context.Context) ([]pgtype.UUID, error)
}

// Name implements Job.
func (j *CheckoutExpiryJob) Name() string { return CheckoutExpiryJobName }

// Run implements Job.
func (j *CheckoutExpiryJob) Run(ctx context.Context) (Result, error) {
	if j.Pool == nil || j.Q == nil {
		return nil, errors.New("checkout_expiry: database not configured")
	}
	res := Result{}
	tenants, err := j.tenants(ctx)
	if err != nil {
		return res, fmt.Errorf("list tenants: %w", err)
	}
	var firstErr error
	for _, tenantID := range tenants {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		res.Add("tenants", 1)
		tctx := tenant.WithTenant(ctx, cart.UUIDString(tenantID))
		if err := j.expireOrders(tctx, tenantID, res); err != nil {
			j.logger().Error().Err(err).Str("tenant_id", cart.UUIDString(tenantID)).Msg("checkout_expiry: phase B failed")
			if firstErr == nil {
				firstErr = err
			}
		}
		if err := j.sweepOrphans(tctx, tenantID, res); err != nil {
			j.logger().Error().Err(err).Str("tenant_id", cart.UUIDString(tenantID)).Msg("checkout_expiry: phase A failed")
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return res, firstErr
}

func (j *CheckoutExpiryJob) tenants(ctx context.Context) ([]pgtype.UUID, error) {
	if j.Tenants != nil {
		return j.Tenants(ctx)
	}
	return j.Q.ListActiveTenantIDs(ctx)
}

// expireOrders is Phase B.
func (j *CheckoutExpiryJob) expireOrders(ctx context.Context, tenantID pgtype.UUID, res Result) error {
	batch := j.batchSize()
	for i := 0; i < maxBatchesPerTenant; i++ {
		ids, err := j.Q.ListExpiredUnpaidOrderCandidates(ctx, dbgen.ListExpiredUnpaidOrderCandidatesParams{
			TenantID:        tenantID,
			LegacyWindowSec: seconds(j.legacyWindow()),
			FallbackTtlSec:  seconds(j.FallbackTTL),
			GraceSec:        seconds(j.Grace),
			ProofHoldSec:    seconds(j.ProofHold),
			BatchSize:       int32(batch),
		})
		if err != nil {
			return fmt.Errorf("list expired orders: %w", err)
		}
		expired := 0
		for _, id := range ids {
			outcome, err := j.expireOrder(ctx, tenantID, id)
			if err != nil {
				res.Add("errors", 1)
				j.logger().Error().Err(err).Str("order_id", cart.UUIDString(id)).Msg("checkout_expiry: expire order failed")
				continue
			}
			res.Add(outcome.counter, 1)
			res.Add("payments_expired", outcome.paymentsExpired)
			res.Add("reservations_released", outcome.reservationsReleased)
			if outcome.expired {
				expired++
				if outcome.silent {
					res.Add("orders_expired_silent", 1)
				}
				if outcome.proofUnreviewed {
					res.Add("orders_expired_proof_unreviewed", 1)
				}
			}
		}
		// Candidates that were skipped would be listed again; stop once a
		// batch is short or made no progress.
		if len(ids) < batch || expired == 0 {
			return nil
		}
	}
	return nil
}

type expiryOutcome struct {
	counter              string
	expired              bool
	silent               bool
	proofUnreviewed      bool
	paymentsExpired      int
	reservationsReleased int
}

// expireOrder expires one order in its own transaction and, after commit,
// emits payment.expired and order.canceled unless the expiry is silent, plus
// the ops-only payment.proof_unreviewed when the order had a payment proof.
func (j *CheckoutExpiryJob) expireOrder(ctx context.Context, tenantID, orderID pgtype.UUID) (expiryOutcome, error) {
	var (
		out        expiryOutcome
		row        dbgen.LockPendingOrderForExpiryRow
		paymentIDs []pgtype.UUID
	)
	err := txretry.Run(ctx, txretry.DefaultAttempts, func(ctx context.Context) error {
		out = expiryOutcome{}
		paymentIDs = nil
		tx, err := j.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		qtx := j.Q.WithTx(tx)

		row, err = qtx.LockPendingOrderForExpiry(ctx, dbgen.LockPendingOrderForExpiryParams{
			ID: orderID, TenantID: tenantID, LegacyWindowSec: seconds(j.legacyWindow()),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				out.counter = "skipped_locked_or_changed"
				return nil
			}
			return fmt.Errorf("lock order: %w", err)
		}
		now := j.now()
		deadline := row.CreatedAt.Time.Add(j.FallbackTTL)
		if row.PaymentDeadline.Valid {
			deadline = row.PaymentDeadline.Time
		}
		effective := deadline.Add(j.Grace)
		if effective.After(now) {
			out.counter = "skipped_renewed"
			return nil
		}
		if row.FirstProofAt.Valid && row.FirstProofAt.Time.After(now.Add(-j.ProofHold)) {
			out.counter = "skipped_proof_hold"
			return nil
		}
		if _, err := qtx.UpdateOrderStatusIfAllowed(ctx, dbgen.UpdateOrderStatusIfAllowedParams{ID: orderID, Status: dbgen.OrderStatusEXPIRED}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				out.counter = "skipped_locked_or_changed"
				return nil
			}
			return fmt.Errorf("expire order: %w", err)
		}
		// A customer who uploaded a transfer proof very likely paid; they and
		// ops are always told, however old the order is.
		out.proofUnreviewed = row.FirstProofAt.Valid
		out.silent = !out.proofUnreviewed && j.SilentAfter > 0 && now.Sub(effective) > j.SilentAfter
		paymentIDs, err = qtx.ExpirePendingPaymentsByOrder(ctx, orderID)
		if err != nil {
			return fmt.Errorf("expire payments: %w", err)
		}
		eventPayload, _ := json.Marshal(map[string]any{"source": "worker", "reason": "payment_deadline", "silent": out.silent})
		for _, pid := range paymentIDs {
			if err := qtx.InsertPaymentEvent(ctx, dbgen.InsertPaymentEventParams{PaymentID: pid, Status: dbgen.PaymentStatusEXPIRED, Payload: eventPayload}); err != nil {
				return fmt.Errorf("insert payment event: %w", err)
			}
		}
		released, err := reservation.ReleaseOrder(ctx, qtx, orderID, dbgen.InventoryReservationStatusRELEASED)
		if err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
		out.counter = "orders_expired"
		out.expired = true
		out.paymentsExpired = len(paymentIDs)
		out.reservationsReleased = released
		return nil
	})
	if err != nil {
		return expiryOutcome{}, err
	}
	if out.expired && !out.silent {
		j.emitExpired(ctx, row, paymentIDs)
	}
	if out.expired && out.proofUnreviewed {
		j.emitProofUnreviewed(ctx, row)
	}
	return out, nil
}

func (j *CheckoutExpiryJob) emitExpired(ctx context.Context, row dbgen.LockPendingOrderForExpiryRow, paymentIDs []pgtype.UUID) {
	if j.Events == nil {
		return
	}
	payload := map[string]any{
		"orderId":  cart.UUIDString(row.ID),
		"tenantId": cart.UUIDString(row.TenantID),
		"status":   string(dbgen.OrderStatusEXPIRED),
		"reason":   "payment_deadline",
		"source":   "worker",
	}
	if row.OrderNumber.Valid {
		payload["orderNumber"] = row.OrderNumber.String
	}
	if row.PaymentID.Valid {
		payload["paymentId"] = cart.UUIDString(row.PaymentID)
	} else if len(paymentIDs) > 0 {
		payload["paymentId"] = cart.UUIDString(paymentIDs[0])
	}
	if row.UserID.Valid {
		payload["userId"] = cart.UUIDString(row.UserID)
		if user, err := j.Q.GetUserByID(ctx, row.UserID); err == nil && user.Email != "" {
			payload["email"] = user.Email
		}
	}
	for _, topic := range []string{events.TopicPaymentExpired, events.TopicOrderCanceled} {
		if _, err := j.Events.Emit(ctx, topic, row.ID, payload); err != nil {
			j.logger().Warn().Err(err).Str("topic", topic).Str("order_id", cart.UUIDString(row.ID)).Msg("checkout_expiry: emit event failed")
		}
	}
}

// emitProofUnreviewed raises the ops signal for an order that expired with a
// transfer proof nobody confirmed. The payload carries no email: the topic is
// customer-silent (events.IsCustomerSilent).
func (j *CheckoutExpiryJob) emitProofUnreviewed(ctx context.Context, row dbgen.LockPendingOrderForExpiryRow) {
	orderID := cart.UUIDString(row.ID)
	j.logger().Warn().
		Str("order_id", orderID).
		Str("tenant_id", cart.UUIDString(row.TenantID)).
		Int32("proof_count", row.ProofCount).
		Time("first_proof_at", row.FirstProofAt.Time).
		Msg("payment.proof_unreviewed: order expired with an unconfirmed transfer proof; review the transfer and refund if received")
	if j.Events == nil {
		return
	}
	payload := map[string]any{
		"orderId":      orderID,
		"tenantId":     cart.UUIDString(row.TenantID),
		"status":       string(dbgen.OrderStatusEXPIRED),
		"firstProofAt": row.FirstProofAt.Time.UTC().Format(time.RFC3339),
		"proofCount":   row.ProofCount,
		"source":       "worker",
	}
	if row.OrderNumber.Valid {
		payload["orderNumber"] = row.OrderNumber.String
	}
	if row.UserID.Valid {
		payload["userId"] = cart.UUIDString(row.UserID)
	}
	if _, err := j.Events.Emit(ctx, events.TopicPaymentProofUnreviewed, row.ID, payload); err != nil {
		j.logger().Warn().Err(err).Str("order_id", orderID).Msg("checkout_expiry: emit payment.proof_unreviewed failed")
	}
}

// sweepOrphans is Phase A.
func (j *CheckoutExpiryJob) sweepOrphans(ctx context.Context, tenantID pgtype.UUID, res Result) error {
	batch := j.batchSize()
	for i := 0; i < maxBatchesPerTenant; i++ {
		var listed, expired int
		err := txretry.Run(ctx, txretry.DefaultAttempts, func(ctx context.Context) error {
			listed, expired = 0, 0
			tx, err := j.Pool.BeginTx(ctx, pgx.TxOptions{})
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback(ctx) }()
			qtx := j.Q.WithTx(tx)
			rows, err := qtx.ListOrphanExpiredReservationsForTenant(ctx, dbgen.ListOrphanExpiredReservationsForTenantParams{TenantID: tenantID, BatchSize: int32(batch)})
			if err != nil {
				return fmt.Errorf("list orphan reservations: %w", err)
			}
			listed = len(rows)
			orders := map[[16]byte]pgtype.UUID{}
			for _, r := range rows {
				if _, err := qtx.TransitionInventoryReservation(ctx, dbgen.TransitionInventoryReservationParams{
					ID: r.ID, FromStatus: dbgen.InventoryReservationStatusRESERVED, ToStatus: dbgen.InventoryReservationStatusEXPIRED,
				}); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					return fmt.Errorf("transition reservation: %w", err)
				}
				if err := qtx.ReleaseVariantStock(ctx, dbgen.ReleaseVariantStockParams{ID: r.VariantID, Qty: r.Qty}); err != nil {
					return fmt.Errorf("release variant stock: %w", err)
				}
				orders[r.OrderID.Bytes] = r.OrderID
				expired++
			}
			if err := reservation.ReleaseFlashSale(ctx, qtx, sortedUUIDs(orders)); err != nil {
				return err
			}
			return tx.Commit(ctx)
		})
		if err != nil {
			return err
		}
		res.Add("reservations_expired", expired)
		if listed < batch || expired == 0 {
			break
		}
	}
	anomalies, err := j.Q.CountAnomalousExpiredReservationsForTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("count anomalous reservations: %w", err)
	}
	if anomalies > 0 {
		res.Add("reservations_anomalous", int(anomalies))
		j.logger().Warn().Int64("count", anomalies).Str("tenant_id", cart.UUIDString(tenantID)).Msg("reservation_anomaly: expired reservations still RESERVED on paid or fulfilled orders")
	}
	return nil
}

func (j *CheckoutExpiryJob) batchSize() int {
	if j.BatchSize <= 0 {
		return 100
	}
	return j.BatchSize
}

func (j *CheckoutExpiryJob) legacyWindow() time.Duration {
	if j.LegacyProviderWindow > 0 {
		return j.LegacyProviderWindow
	}
	return DefaultLegacyProviderWindow
}

func (j *CheckoutExpiryJob) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *CheckoutExpiryJob) logger() *zerolog.Logger {
	if j.Logger != nil {
		return j.Logger
	}
	nop := zerolog.Nop()
	return &nop
}

func seconds(d time.Duration) int32 {
	if d <= 0 {
		return 0
	}
	return int32(d / time.Second)
}

func sortedUUIDs(m map[[16]byte]pgtype.UUID) []pgtype.UUID {
	out := make([]pgtype.UUID, 0, len(m))
	for _, id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(a, b int) bool { return bytes.Compare(out[a].Bytes[:], out[b].Bytes[:]) < 0 })
	return out
}
