package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/db/txretry"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

const maxConfirmNoteLen = 500

type confirmPaymentRequest struct {
	Note string `json:"note"`
}

type confirmPaymentResponse struct {
	OrderID       string `json:"orderId"`
	OrderStatus   string `json:"orderStatus"`
	PaymentID     string `json:"paymentId"`
	PaymentStatus string `json:"paymentStatus"`
}

// AdminConfirmPayment handles POST /api/v1/admin/orders/{id}/confirm-payment.
//
// It is the recovery path for manual bank transfers: after an admin has
// verified the customer's transfer (usually from an uploaded payment proof),
// the order is settled exactly like a PAID provider callback, under the same
// order row lock: payment PAID with a payment_events row (source "admin"),
// status guard PENDING_PAYMENT->PAID, reservations and flash-sale quota
// committed, voucher settled, order.paid emitted. Only PENDING_PAYMENT orders
// of the caller's tenant can be confirmed; EXPIRED or CANCELLED orders return
// 409 because their stock has already been released.
//
// The route sits in the admin group, so the admin role check and the audit
// log middleware apply.
func (h Webhook) AdminConfirmPayment(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil || h.Pool == nil {
		common.JSONError(w, http.StatusInternalServerError, "PAYMENT_NOT_CONFIGURED", "payment confirmation unavailable", nil)
		return
	}
	ctx := r.Context()
	tenantRaw, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "TENANT_REQUIRED", "tenant is required", nil)
		return
	}
	tenantID, err := cart.ToUUID(tenantRaw)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "TENANT_REQUIRED", "invalid tenant", nil)
		return
	}
	orderID, err := cart.ToUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	var req confirmPaymentRequest
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
			return
		}
	}
	req.Note = strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(req.Note) > maxConfirmNoteLen {
		common.JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", fmt.Sprintf("note must be at most %d characters", maxConfirmNoteLen), nil)
		return
	}
	actorID, _ := common.UserID(ctx)
	eventPayload, _ := json.Marshal(map[string]any{"source": "admin", "reason": "manual_confirmation", "actorId": actorID, "note": req.Note})

	var res settlementResult
	err = txretry.Run(ctx, txretry.DefaultAttempts, func(ctx context.Context) error {
		var txErr error
		res, txErr = h.confirmManualPayment(ctx, tenantID, orderID, eventPayload)
		return txErr
	})
	if err != nil {
		var we *webhookError
		if errors.As(err, &we) {
			common.JSONError(w, we.status, we.code, we.message, nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to confirm payment", nil)
		return
	}
	h.afterSettlement(ctx, res.slugs)
	h.emitCallbackEvents(ctx, "admin", res)
	common.JSON(w, http.StatusOK, map[string]any{"data": confirmPaymentResponse{
		OrderID:       cart.UUIDString(res.order.ID),
		OrderStatus:   string(dbgen.OrderStatusPAID),
		PaymentID:     cart.UUIDString(res.payment.ID),
		PaymentStatus: string(dbgen.PaymentStatusPAID),
	}})
}

func (h Webhook) confirmManualPayment(ctx context.Context, tenantID, orderID pgtype.UUID, eventPayload []byte) (settlementResult, error) {
	res := settlementResult{newStatus: dbgen.PaymentStatusPAID}
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := h.Q.WithTx(tx)

	order, err := q.GetOrderByIDForUpdate(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return res, fail(http.StatusNotFound, "NOT_FOUND", "order not found")
		}
		return res, fmt.Errorf("lock order: %w", err)
	}
	if order.TenantID != tenantID {
		return res, fail(http.StatusNotFound, "NOT_FOUND", "order not found")
	}
	if order.Status != dbgen.OrderStatusPENDINGPAYMENT {
		return res, fail(http.StatusConflict, "INVALID_STATE", "only pending_payment orders can be confirmed")
	}
	res.order = order

	payment, err := q.GetLatestPaymentByOrder(ctx, orderID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		payment, err = q.CreatePayment(ctx, dbgen.CreatePaymentParams{
			OrderID:         orderID,
			Provider:        pgtype.Text{String: "manual", Valid: true},
			Status:          dbgen.PaymentStatusPAID,
			ProviderPayload: eventPayload,
			Amount:          pgtype.Int8{Int64: order.PricingTotal, Valid: true},
			TenantID:        order.TenantID,
		})
		if err != nil {
			return res, fmt.Errorf("create payment: %w", err)
		}
	case err != nil:
		return res, fmt.Errorf("load payment: %w", err)
	case payment.Status == dbgen.PaymentStatusREFUNDED:
		return res, fail(http.StatusConflict, "INVALID_STATE", "the latest payment was refunded")
	case payment.Status != dbgen.PaymentStatusPAID:
		if err := q.UpdatePaymentStatus(ctx, dbgen.UpdatePaymentStatusParams{ID: payment.ID, Status: dbgen.PaymentStatusPAID, ProviderPayload: eventPayload}); err != nil {
			return res, fmt.Errorf("update payment: %w", err)
		}
	}
	if err := q.InsertPaymentEvent(ctx, dbgen.InsertPaymentEventParams{PaymentID: payment.ID, Status: dbgen.PaymentStatusPAID, Payload: eventPayload}); err != nil {
		return res, fmt.Errorf("insert payment event: %w", err)
	}
	res.payment = payment
	res.paymentChanged = true

	settled, slugs, notCommitted, err := h.settlePaid(ctx, q, order)
	if err != nil {
		return res, err
	}
	if !settled {
		return res, fail(http.StatusConflict, "INVALID_STATE", "only pending_payment orders can be confirmed")
	}
	res.settled, res.slugs, res.notCommittedRows = true, slugs, notCommitted
	res.order.Status = dbgen.OrderStatusPAID
	if err := tx.Commit(ctx); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}
