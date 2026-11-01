package payment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	redis "github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/db/txretry"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/obs"
	"github.com/noah-isme/backend-toko/internal/reservation"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Webhook handles payment provider callbacks, including signature verification and settlement.
type Webhook struct {
	Q            *dbgen.Queries
	Pool         *pgxpool.Pool
	Providers    map[string]Provider
	Replay       *redis.Client
	ReplayTTL    time.Duration
	Voucher      VoucherSettler
	Events       *events.Bus
	CatalogCache *catalog.Cache
	Analytics    *analytics.Service
	// Logger is optional; it reports late payments and settlement anomalies.
	Logger *zerolog.Logger
}

// VoucherSettler records voucher usage as part of order settlement.
type VoucherSettler interface {
	Settle(ctx context.Context, code string, orderID pgtype.UUID, userID pgtype.UUID, amount int64) error
}

// webhookError is a handled failure that maps to an HTTP error response.
type webhookError struct {
	status  int
	code    string
	message string
}

func (e *webhookError) Error() string { return e.code + ": " + e.message }

func fail(status int, code, message string) error {
	return &webhookError{status: status, code: code, message: message}
}

// settlementResult describes what one settlement transaction changed. Events
// are emitted from it after commit, and only for real state changes.
type settlementResult struct {
	order            dbgen.Order
	payment          dbgen.Payment
	newStatus        dbgen.PaymentStatus
	paymentChanged   bool
	settled          bool
	late             bool
	orderCanceled    bool
	refundEventOnly  bool
	slugs            []string
	notCommittedRows int
}

// statusRecorder captures the response status so the replay key can be
// dropped when the request did not succeed.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Handle processes webhook callbacks for the configured payment provider(s).
//
// Race safety: the settlement transaction locks the order row first (see the
// lock order in internal/reservation), so it serialises with the
// checkout-expiry job, customer cancel and admin confirmation. Payment rows
// and events change only on a real state transition, which makes provider
// retries and duplicate callbacks (Midtrans capture + settlement) idempotent.
// A PAID callback for an order that is no longer PENDING_PAYMENT takes the
// late-payment path: the payment is recorded PAID, the order is left alone,
// payment.late is emitted for ops, and the response is 204.
func (h Webhook) Handle(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil || h.Providers == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "webhook unavailable", nil)
		return
	}
	ctx, span := otel.Tracer("payment.Webhook").Start(r.Context(), "PaymentWebhook.Handle")
	defer span.End()
	r = r.WithContext(ctx)

	providerKey := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "provider")))
	providerLabel := normaliseLabel(providerKey)
	outcome := "error"
	defer func() {
		if obs.PaymentWebhookTotal != nil {
			obs.PaymentWebhookTotal.WithLabelValues(providerLabel, outcome).Inc()
		}
	}()
	span.SetAttributes(attribute.String("payment.webhook.provider", providerKey))

	provider, ok := h.Providers[providerKey]
	if !ok {
		span.RecordError(errors.New("unknown provider"))
		common.JSONError(w, http.StatusNotFound, "PROVIDER_NOT_SUPPORTED", "unknown provider", nil)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "unable to read payload", nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	result, err := provider.VerifyWebhook(r, body)
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusBadRequest, "WEBHOOK_INVALID", err.Error(), nil)
		return
	}
	if !result.Valid {
		span.RecordError(errors.New("invalid signature"))
		common.JSONError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "signature verification failed", nil)
		return
	}
	rec := &statusRecorder{ResponseWriter: w}
	w = rec
	if h.Replay != nil && h.ReplayTTL > 0 {
		key := fmt.Sprintf("wh:%s:%s", providerKey, common.Sha256Hex(string(body)))
		ok, err := h.Replay.SetNX(r.Context(), key, "1", h.ReplayTTL).Result()
		if err != nil {
			span.RecordError(err)
			common.JSONError(w, http.StatusInternalServerError, "REPLAY_STORE_ERROR", err.Error(), nil)
			return
		}
		if !ok {
			span.AddEvent("payment webhook replay prevented")
			common.JSONError(w, http.StatusConflict, "REPLAY", "duplicate webhook", nil)
			return
		}
		// Only a successful delivery is remembered: when this request fails
		// the provider's retry with the same body must be processed.
		defer func() {
			if rec.status < 200 || rec.status >= 300 {
				_ = h.Replay.Del(context.Background(), key).Err()
			}
		}()
	}
	if result.ProviderPayload == nil {
		result.ProviderPayload = body
	}
	orderUUID, err := cart.ToUUID(result.OrderID)
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusBadRequest, "INVALID_ORDER_ID", "invalid order identifier", nil)
		return
	}
	span.SetAttributes(attribute.String("order.id", result.OrderID))
	newStatus := normaliseWebhookStatus(result.Status)
	span.SetAttributes(attribute.String("payment.webhook.status", string(newStatus)))

	var res settlementResult
	err = txretry.Run(ctx, txretry.DefaultAttempts, func(ctx context.Context) error {
		var txErr error
		res, txErr = h.applyCallback(ctx, orderUUID, newStatus, result)
		return txErr
	})
	if err != nil {
		span.RecordError(err)
		var we *webhookError
		if errors.As(err, &we) {
			common.JSONError(w, we.status, we.code, we.message, nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "SETTLEMENT_ERROR", err.Error(), nil)
		return
	}
	if res.refundEventOnly {
		span.SetAttributes(attribute.Bool("payment.webhook.refund_event_only", true))
	}
	if res.settled {
		h.afterSettlement(ctx, res.slugs)
	}
	h.emitCallbackEvents(ctx, providerLabel, res)
	outcome = "success"
	if res.late {
		outcome = "late"
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyCallback runs the settlement transaction for one provider callback.
func (h Webhook) applyCallback(ctx context.Context, orderID pgtype.UUID, newStatus dbgen.PaymentStatus, result WebhookVerifyResult) (settlementResult, error) {
	res := settlementResult{newStatus: newStatus}
	q := h.Q
	var tx pgx.Tx
	if h.Pool != nil {
		var err error
		tx, err = h.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return res, fmt.Errorf("begin: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		q = h.Q.WithTx(tx)
	}
	order, err := q.GetOrderByIDForUpdate(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, fail(http.StatusNotFound, "ORDER_NOT_FOUND", "order not found")
		}
		return res, fmt.Errorf("lock order: %w", err)
	}
	res.order = order
	payment, err := q.GetLatestPaymentByOrder(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, fail(http.StatusNotFound, "PAYMENT_NOT_FOUND", "payment not found")
		}
		return res, fmt.Errorf("load payment: %w", err)
	}
	res.payment = payment
	if result.Amount > 0 && payment.Amount.Valid && payment.Amount.Int64 != result.Amount {
		return res, fail(http.StatusBadRequest, "AMOUNT_MISMATCH", "provider amount mismatch")
	}
	if isRefundWebhookStatus(newStatus) || isRefundWebhookStatus(payment.Status) {
		// Refund notifications, and any notification for a payment that has already
		// been (partially) refunded, are recorded as events only: the admin refund
		// flow is the single writer of payments.status and payments.refunded_amount.
		// A re-delivered settlement therefore cannot move a refunded payment back to
		// PAID, re-settle the order and voucher, or emit order.paid again.
		if err := q.InsertPaymentEvent(ctx, dbgen.InsertPaymentEventParams{
			PaymentID: payment.ID,
			Status:    newStatus,
			Payload:   result.ProviderPayload,
		}); err != nil {
			return res, fmt.Errorf("insert payment event: %w", err)
		}
		res.refundEventOnly = true
		if tx != nil {
			if err := tx.Commit(ctx); err != nil {
				return res, fmt.Errorf("commit: %w", err)
			}
		}
		return res, nil
	}
	if paymentTransitionAllowed(payment.Status, newStatus) {
		if err := q.UpdatePaymentStatus(ctx, dbgen.UpdatePaymentStatusParams{
			ID:              payment.ID,
			Status:          newStatus,
			ProviderPayload: result.ProviderPayload,
		}); err != nil {
			return res, fmt.Errorf("update payment: %w", err)
		}
		if err := q.InsertPaymentEvent(ctx, dbgen.InsertPaymentEventParams{
			PaymentID: payment.ID,
			Status:    newStatus,
			Payload:   result.ProviderPayload,
		}); err != nil {
			return res, fmt.Errorf("insert payment event: %w", err)
		}
		res.paymentChanged = true
	}
	if res.paymentChanged {
		switch newStatus {
		case dbgen.PaymentStatusPAID:
			settled, slugs, notCommitted, err := h.settlePaid(ctx, q, order)
			if err != nil {
				return res, err
			}
			res.settled, res.slugs, res.notCommittedRows = settled, slugs, notCommitted
			res.late = !settled
			if settled {
				res.order.Status = dbgen.OrderStatusPAID
			}
		case dbgen.PaymentStatusFAILED, dbgen.PaymentStatusEXPIRED:
			if order.Status == dbgen.OrderStatusPENDINGPAYMENT {
				target := dbgen.OrderStatusCANCELLED
				if newStatus == dbgen.PaymentStatusEXPIRED {
					target = dbgen.OrderStatusEXPIRED
				}
				if _, err := q.UpdateOrderStatusIfAllowed(ctx, dbgen.UpdateOrderStatusIfAllowedParams{ID: order.ID, Status: target}); err != nil {
					if !errors.Is(err, pgx.ErrNoRows) {
						return res, fmt.Errorf("update order status: %w", err)
					}
				} else {
					if _, err := reservation.ReleaseOrder(ctx, q, order.ID, dbgen.InventoryReservationStatusRELEASED); err != nil {
						return res, err
					}
					res.orderCanceled = true
					res.order.Status = target
				}
			}
		}
	}
	if tx != nil {
		if err := tx.Commit(ctx); err != nil {
			return res, fmt.Errorf("commit: %w", err)
		}
	}
	return res, nil
}

// settlePaid moves a PENDING_PAYMENT order to PAID through the status guard
// and commits its reservations, flash-sale quota and voucher. It returns
// settled=false, without touching anything, when the order is no longer
// payable. The caller holds the order row lock.
func (h Webhook) settlePaid(ctx context.Context, q *dbgen.Queries, order dbgen.Order) (bool, []string, int, error) {
	if _, err := q.UpdateOrderStatusIfAllowed(ctx, dbgen.UpdateOrderStatusIfAllowedParams{ID: order.ID, Status: dbgen.OrderStatusPAID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil, 0, nil
		}
		return false, nil, 0, fmt.Errorf("update order status: %w", err)
	}
	items, err := q.ListOrderItemsForStock(ctx, order.ID)
	if err != nil {
		return false, nil, 0, fmt.Errorf("list order items: %w", err)
	}
	reservations, err := q.ListInventoryReservationsByOrder(ctx, order.ID)
	if err != nil {
		return false, nil, 0, fmt.Errorf("list reservations: %w", err)
	}
	seen := map[string]struct{}{}
	var slugs []string
	for _, it := range items {
		if slug := strings.TrimSpace(it.Slug); slug != "" {
			if _, ok := seen[slug]; !ok {
				seen[slug] = struct{}{}
				slugs = append(slugs, slug)
			}
		}
	}
	notCommitted := 0
	for _, res := range reservations {
		if res.Status != dbgen.InventoryReservationStatusRESERVED {
			if res.Status != dbgen.InventoryReservationStatusCOMMITTED {
				notCommitted++
			}
			continue
		}
		if _, err := q.TransitionInventoryReservation(ctx, dbgen.TransitionInventoryReservationParams{
			ID:         res.ID,
			FromStatus: dbgen.InventoryReservationStatusRESERVED,
			ToStatus:   dbgen.InventoryReservationStatusCOMMITTED,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notCommitted++
				continue
			}
			return false, nil, 0, fmt.Errorf("commit reservation: %w", err)
		}
	}
	if err := q.CommitFlashSaleReservations(ctx, order.ID); err != nil {
		return false, nil, 0, fmt.Errorf("commit flash-sale reservations: %w", err)
	}
	if h.Voucher != nil && order.AppliedVoucherCode.Valid {
		if code := strings.TrimSpace(order.AppliedVoucherCode.String); code != "" {
			amount := order.PricingDiscount
			if amount < 0 {
				amount = 0
			}
			if err := h.Voucher.Settle(ctx, code, order.ID, order.UserID, amount); err != nil {
				return false, nil, 0, fmt.Errorf("settle voucher: %w", err)
			}
		}
	}
	return true, slugs, notCommitted, nil
}

func (h Webhook) afterSettlement(ctx context.Context, slugs []string) {
	for _, slug := range slugs {
		h.invalidateProductCache(ctx, slug)
	}
	h.clearAnalyticsCache(ctx)
}

// emitCallbackEvents emits domain events for the state changes of one
// callback. Outbound webhooks are routed by the order's tenant.
func (h Webhook) emitCallbackEvents(ctx context.Context, providerLabel string, res settlementResult) {
	order := res.order
	if order.TenantID.Valid {
		ctx = tenant.WithTenant(ctx, cart.UUIDString(order.TenantID))
	}
	logger := h.logger()
	if res.notCommittedRows > 0 {
		logger.Warn().Str("order_id", cart.UUIDString(order.ID)).Int("count", res.notCommittedRows).
			Msg("reservation_not_committed: order paid but some reservations were no longer RESERVED")
	}
	if res.late {
		if obs.PaymentLateTotal != nil {
			obs.PaymentLateTotal.WithLabelValues(providerLabel).Inc()
		}
		logger.Warn().
			Str("order_id", cart.UUIDString(order.ID)).
			Str("payment_id", cart.UUIDString(res.payment.ID)).
			Str("tenant_id", cart.UUIDString(order.TenantID)).
			Str("order_status", string(order.Status)).
			Str("provider", providerLabel).
			Msg("payment.late: payment captured for an order that is no longer payable; refund required")
	}
	if h.Events == nil || !res.paymentChanged {
		return
	}
	if res.late {
		// No email or other recipient field: payment.late must never notify
		// the customer (EmailNotifier sends to any payload "email").
		payload := map[string]any{
			"orderId":     cart.UUIDString(order.ID),
			"paymentId":   cart.UUIDString(res.payment.ID),
			"tenantId":    cart.UUIDString(order.TenantID),
			"status":      string(dbgen.PaymentStatusPAID),
			"orderStatus": string(order.Status),
		}
		if order.UserID.Valid {
			payload["userId"] = cart.UUIDString(order.UserID)
		}
		if _, err := h.Events.Emit(ctx, events.TopicPaymentLate, order.ID, payload); err != nil {
			logger.Warn().Err(err).Str("order_id", cart.UUIDString(order.ID)).Msg("emit payment.late failed")
		}
		return
	}
	payload := map[string]any{
		"orderId":   cart.UUIDString(order.ID),
		"paymentId": cart.UUIDString(res.payment.ID),
		"tenantId":  cart.UUIDString(order.TenantID),
		"status":    string(res.newStatus),
	}
	if order.UserID.Valid {
		payload["userId"] = cart.UUIDString(order.UserID)
		if user, err := h.Q.GetUserByID(ctx, order.UserID); err == nil && user.Email != "" {
			payload["email"] = user.Email
		}
	}
	var topics []string
	switch res.newStatus {
	case dbgen.PaymentStatusPAID:
		if res.settled {
			topics = append(topics, events.TopicOrderPaid)
		}
	case dbgen.PaymentStatusFAILED:
		topics = append(topics, events.TopicPaymentFailed)
	case dbgen.PaymentStatusEXPIRED:
		topics = append(topics, events.TopicPaymentExpired)
	}
	if res.orderCanceled {
		topics = append(topics, events.TopicOrderCanceled)
	}
	for _, topic := range topics {
		if _, err := h.Events.Emit(ctx, topic, order.ID, payload); err != nil {
			logger.Warn().Err(err).Str("topic", topic).Str("order_id", cart.UUIDString(order.ID)).Msg("emit payment event failed")
		}
	}
}

// paymentTransitionAllowed decides whether a callback changes the payment.
// PAID is applied unless the payment is already PAID, PARTIALLY_REFUNDED or
// REFUNDED (so a late payment after the worker expired the intent is still
// recorded); FAILED and EXPIRED only replace PENDING; PENDING and the refund
// statuses never overwrite anything (applyCallback records refund
// notifications as events only; the admin refund flow owns refund state).
func paymentTransitionAllowed(current, next dbgen.PaymentStatus) bool {
	switch next {
	case dbgen.PaymentStatusPAID:
		return current != dbgen.PaymentStatusPAID && current != dbgen.PaymentStatusREFUNDED &&
			current != dbgen.PaymentStatusPARTIALLYREFUNDED
	case dbgen.PaymentStatusFAILED, dbgen.PaymentStatusEXPIRED:
		return current == dbgen.PaymentStatusPENDING
	default:
		return false
	}
}

func (h Webhook) logger() *zerolog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	nop := zerolog.Nop()
	return &nop
}

func (h Webhook) invalidateProductCache(ctx context.Context, slug string) {
	if strings.TrimSpace(slug) == "" {
		return
	}
	if h.CatalogCache != nil {
		h.CatalogCache.InvalidateProduct(ctx, slug)
	}
}

func (h Webhook) clearAnalyticsCache(ctx context.Context) {
	if h.Analytics != nil {
		h.Analytics.Clear(ctx)
	}
}

func normaliseWebhookStatus(status string) dbgen.PaymentStatus {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PAID", "SUCCESS", "SETTLED":
		return dbgen.PaymentStatusPAID
	case "FAILED", "CANCELED", "DENY":
		return dbgen.PaymentStatusFAILED
	case "EXPIRED":
		return dbgen.PaymentStatusEXPIRED
	case "REFUNDED", "REFUND":
		return dbgen.PaymentStatusREFUNDED
	case "PARTIALLY_REFUNDED", "PARTIAL_REFUND":
		return dbgen.PaymentStatusPARTIALLYREFUNDED
	default:
		return dbgen.PaymentStatusPENDING
	}
}

// isRefundWebhookStatus reports whether a normalised webhook status is a refund
// notification, which is recorded as a payment event without changing the payment.
func isRefundWebhookStatus(s dbgen.PaymentStatus) bool {
	return s == dbgen.PaymentStatusREFUNDED || s == dbgen.PaymentStatusPARTIALLYREFUNDED
}
