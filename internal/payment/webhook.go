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

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// Webhook handles payment provider callbacks, including signature verification and settlement.
type Webhook struct {
	Q         *dbgen.Queries
	Pool      *pgxpool.Pool
	Providers map[string]Provider
	Replay    *redis.Client
	ReplayTTL time.Duration
	Voucher   VoucherSettler
	Events    *events.Bus
}

// VoucherSettler records voucher usage as part of order settlement.
type VoucherSettler interface {
	Settle(ctx context.Context, code string, orderID pgtype.UUID, userID pgtype.UUID, amount int64) error
}

// Handle processes webhook callbacks for the configured payment provider(s).
func (h Webhook) Handle(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil || h.Providers == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "webhook unavailable", nil)
		return
	}
	providerKey := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "provider")))
	provider, ok := h.Providers[providerKey]
	if !ok {
		common.JSONError(w, http.StatusNotFound, "PROVIDER_NOT_SUPPORTED", "unknown provider", nil)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "unable to read payload", nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	result, err := provider.VerifyWebhook(r, body)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "WEBHOOK_INVALID", err.Error(), nil)
		return
	}
	if !result.Valid {
		common.JSONError(w, http.StatusUnauthorized, "INVALID_SIGNATURE", "signature verification failed", nil)
		return
	}
	if h.Replay != nil && h.ReplayTTL > 0 {
		key := fmt.Sprintf("wh:%s:%s", providerKey, common.Sha256Hex(string(body)))
		ok, err := h.Replay.SetNX(r.Context(), key, "1", h.ReplayTTL).Result()
		if err != nil {
			common.JSONError(w, http.StatusInternalServerError, "REPLAY_STORE_ERROR", err.Error(), nil)
			return
		}
		if !ok {
			common.JSONError(w, http.StatusConflict, "REPLAY", "duplicate webhook", nil)
			return
		}
	}
	if result.ProviderPayload == nil {
		result.ProviderPayload = body
	}
	orderUUID, err := cart.ToUUID(result.OrderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_ORDER_ID", "invalid order identifier", nil)
		return
	}
	ctx := r.Context()
	q := h.Q
	var tx pgx.Tx
	if h.Pool != nil {
		tx, err = h.Pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			common.JSONError(w, http.StatusInternalServerError, "TX_ERROR", err.Error(), nil)
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()
		q = h.Q.WithTx(tx)
	}

	payment, err := q.GetLatestPaymentByOrder(ctx, orderUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "PAYMENT_NOT_FOUND", "payment not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_FETCH_ERROR", err.Error(), nil)
		return
	}
	if result.Amount > 0 && payment.Amount.Valid && payment.Amount.Int64 != result.Amount {
		common.JSONError(w, http.StatusBadRequest, "AMOUNT_MISMATCH", "provider amount mismatch", nil)
		return
	}
	newStatus := normaliseWebhookStatus(result.Status)
	shouldSettle := newStatus == dbgen.PaymentStatusPAID && payment.Status != dbgen.PaymentStatusPAID

	if err := q.UpdatePaymentStatus(ctx, dbgen.UpdatePaymentStatusParams{
		ID:              payment.ID,
		Status:          newStatus,
		ProviderPayload: result.ProviderPayload,
	}); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_UPDATE_ERROR", err.Error(), nil)
		return
	}
	_ = q.InsertPaymentEvent(ctx, dbgen.InsertPaymentEventParams{
		PaymentID: payment.ID,
		Status:    newStatus,
		Payload:   result.ProviderPayload,
	})

	order, err := q.GetOrderByID(ctx, payment.OrderID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "ORDER_FETCH_ERROR", err.Error(), nil)
		return
	}
	orderCanceled := false
	switch newStatus {
	case dbgen.PaymentStatusPAID:
		if shouldSettle {
			if err := q.UpdateOrderStatus(ctx, dbgen.UpdateOrderStatusParams{ID: order.ID, Status: dbgen.OrderStatusPAID}); err != nil {
				common.JSONError(w, http.StatusInternalServerError, "ORDER_UPDATE_ERROR", err.Error(), nil)
				return
			}
			items, err := q.ListOrderItemsForStock(ctx, order.ID)
			if err != nil {
				common.JSONError(w, http.StatusInternalServerError, "ORDER_ITEMS_ERROR", err.Error(), nil)
				return
			}
			for _, it := range items {
				if it.VariantID.Valid {
					if err := q.DecrementVariantStock(ctx, dbgen.DecrementVariantStockParams{Qty: int32(it.Qty), ID: it.VariantID}); err != nil {
						common.JSONError(w, http.StatusInternalServerError, "STOCK_UPDATE_ERROR", err.Error(), nil)
						return
					}
				}
			}
			if h.Voucher != nil && order.AppliedVoucherCode.Valid {
				code := strings.TrimSpace(order.AppliedVoucherCode.String)
				if code != "" {
					amount := order.PricingDiscount
					if amount < 0 {
						amount = 0
					}
					if err := h.Voucher.Settle(ctx, code, order.ID, order.UserID, amount); err != nil {
						common.JSONError(w, http.StatusInternalServerError, "VOUCHER_SETTLEMENT_FAILED", err.Error(), nil)
						return
					}
				}
			}
		}
	case dbgen.PaymentStatusFAILED, dbgen.PaymentStatusEXPIRED:
		if order.Status == dbgen.OrderStatusPENDINGPAYMENT {
			if err := q.UpdateOrderStatus(ctx, dbgen.UpdateOrderStatusParams{ID: order.ID, Status: dbgen.OrderStatusCANCELED}); err == nil {
				orderCanceled = true
				order.Status = dbgen.OrderStatusCANCELED
			}
		}
	}

	if tx != nil {
		if err := tx.Commit(ctx); err != nil {
			common.JSONError(w, http.StatusInternalServerError, "TX_COMMIT_ERROR", err.Error(), nil)
			return
		}
	}
	if h.Events != nil {
		payload := map[string]any{
			"orderId":   cart.UUIDString(order.ID),
			"paymentId": cart.UUIDString(payment.ID),
			"status":    string(newStatus),
		}
		if order.UserID.Valid {
			payload["userId"] = cart.UUIDString(order.UserID)
		}
		if user, err := h.Q.GetUserByID(ctx, order.UserID); err == nil && user.Email != "" {
			payload["email"] = user.Email
		}
		switch newStatus {
		case dbgen.PaymentStatusPAID:
			_, _ = h.Events.Emit(ctx, events.TopicOrderPaid, order.ID, payload)
		case dbgen.PaymentStatusFAILED:
			_, _ = h.Events.Emit(ctx, events.TopicPaymentFailed, order.ID, payload)
			if orderCanceled {
				_, _ = h.Events.Emit(ctx, events.TopicOrderCanceled, order.ID, payload)
			}
		case dbgen.PaymentStatusEXPIRED:
			_, _ = h.Events.Emit(ctx, events.TopicPaymentExpired, order.ID, payload)
			if orderCanceled {
				_, _ = h.Events.Emit(ctx, events.TopicOrderCanceled, order.ID, payload)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func normaliseWebhookStatus(status string) dbgen.PaymentStatus {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "PAID", "SUCCESS", "SETTLED":
		return dbgen.PaymentStatusPAID
	case "FAILED", "CANCELED", "DENY":
		return dbgen.PaymentStatusFAILED
	case "EXPIRED":
		return dbgen.PaymentStatusEXPIRED
	default:
		return dbgen.PaymentStatusPENDING
	}
}
