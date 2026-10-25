package platform

// Admin refunds run in three phases so a refund can never be lost or issued twice:
//
//	A. (tx, request context) lock the return and its refunds row, decide the mode,
//	   reserve the attempt by committing a PENDING refunds row. Its id and attempt
//	   number form the gateway key "rf-<id>-<attempt>".
//	B. (detached context, own budget) call the gateway, or look the key up first
//	   when a previous call with the same key has an unknown outcome.
//	C. (tx, fresh detached context) finalise refunds, returns and payments together.
//
// If C fails after the gateway accepted the refund, the provider ref is stored on
// the refunds row (settled_at stays NULL) and the next request reconciles without
// calling the gateway again. Only settled_at IS NOT NULL means local records were
// finalised; refunds.amount may be less than the order total (partial refund).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/cart"
	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/payment"
)

// Variables (not constants) only so tests can shorten them (export_test.go).
var (
	// refundGatewayTimeout bounds Phase B (lookup + refund call).
	refundGatewayTimeout = 25 * time.Second
	// refundWriteTimeout bounds each bookkeeping write and the Phase C transaction.
	// Each gets a fresh budget so a slow gateway cannot starve them.
	refundWriteTimeout = 10 * time.Second
)

const (
	// refundInFlightSeconds: a reserved attempt younger than this is assumed to be
	// mid-gateway-call. It must exceed refundGatewayTimeout + refundWriteTimeout.
	refundInFlightSeconds = 60
	defaultRefundReason   = "customer return"
	maxRefundReasonRunes  = 500
)

type refundInput struct {
	Amount *int64  `json:"amount"`
	Reason *string `json:"reason"`
}

type refundMode int

const (
	refundModeFresh        refundMode = iota // no refunds row yet
	refundModeNewAttempt                     // previous attempt definitively failed: new key
	refundModeRetrySameKey                   // previous attempt has unknown outcome: same key, lookup first
	refundModeReconcile                      // gateway accepted, local records not finalised
)

type refundAttempt struct {
	mode           refundMode
	tenantID       pgtype.UUID
	refundID       pgtype.UUID
	returnID       pgtype.UUID
	orderID        pgtype.UUID
	paymentID      pgtype.UUID
	provider       string
	paymentRef     string
	amount         int64
	reason         string
	attempt        int32
	providerRef    string
	providerStatus string
	status         string
	gateway        payment.RefundProvider
}

func (a refundAttempt) key() string {
	return fmt.Sprintf("rf-%s-%d", cart.UUIDString(a.refundID), a.attempt)
}

type refundOutcome struct {
	createdAt        time.Time
	paymentStatus    string
	refundedAmount   int64
	refundableAmount int64
}

var errRefundAlreadySettled = errors.New("refund already settled by a concurrent request")

type apiError struct {
	status  int
	code    string
	message string
	details any
}

func (e *apiError) write(w http.ResponseWriter) {
	common.JSONError(w, e.status, e.code, e.message, e.details)
}

func internalError(message string) *apiError {
	return &apiError{status: http.StatusInternalServerError, code: "INTERNAL", message: message}
}

// detachedContext keeps the request's values (logger, trace) but not its
// cancellation: once the gateway may have moved money, a closed admin tab or a
// proxy timeout must not abort the bookkeeping.
func detachedContext(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
}

func decodeRefundInput(r *http.Request) (refundInput, *apiError) {
	var input refundInput
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return input, &apiError{status: 400, code: "BAD_REQUEST", message: "invalid payload"}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(body, &input); err != nil {
		return input, &apiError{status: 400, code: "BAD_REQUEST", message: "invalid payload"}
	}
	if input.Amount != nil && *input.Amount <= 0 {
		return input, &apiError{status: 400, code: "BAD_REQUEST", message: "amount must be a positive integer"}
	}
	if input.Reason != nil && utf8.RuneCountInString(strings.TrimSpace(*input.Reason)) > maxRefundReasonRunes {
		return input, &apiError{status: 400, code: "BAD_REQUEST", message: "reason must be at most 500 characters"}
	}
	return input, nil
}

// AdminRefund issues (or completes) the refund of one return. The body is optional:
// {"amount"?: int, "reason"?: string}; without amount the remaining refundable
// amount of the order's payment is refunded.
func (h *Handler) AdminRefund(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	returnID, err := cart.ToUUID(chi.URLParam(r, "returnId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid return id", nil)
		return
	}
	input, apiErr := decodeRefundInput(r)
	if apiErr != nil {
		apiErr.write(w)
		return
	}

	att, apiErr := h.reserveRefund(r.Context(), tenantID, returnID, input)
	if apiErr != nil {
		apiErr.write(w)
		return
	}
	log := zerolog.Ctx(r.Context()).With().
		Str("refund_id", cart.UUIDString(att.refundID)).
		Str("return_id", cart.UUIDString(att.returnID)).
		Str("order_id", cart.UUIDString(att.orderID)).
		Str("payment_id", cart.UUIDString(att.paymentID)).
		Str("tenant_id", cart.UUIDString(att.tenantID)).
		Str("provider", att.provider).
		Int32("attempt", att.attempt).
		Int64("amount", att.amount).
		Logger()

	if att.mode != refundModeReconcile {
		res, gwErr := h.callRefundGateway(r, att)
		if gwErr != nil {
			h.recordGatewayFailure(r, &log, att, gwErr)
			definite := payment.IsDefiniteRefundFailure(gwErr)
			common.JSONError(w, http.StatusBadGateway, "REFUND_FAILED", gwErr.Error(),
				map[string]any{"refundId": cart.UUIDString(att.refundID), "retryable": !definite})
			return
		}
		att.providerRef = res.ProviderRef
		att.providerStatus = res.Status
		att.status = res.Status
	}

	outcome, err := h.finalizeRefund(r, att)
	if errors.Is(err, errRefundAlreadySettled) {
		// A concurrent request for the same return finalised it first.
		common.JSONError(w, 409, "REFUND_ALREADY_ISSUED", "this return has already been refunded", nil)
		return
	}
	if err != nil {
		log.Error().Err(err).Str("provider_ref", att.providerRef).
			Msg("refund accepted by gateway but local records were not finalised; reconcile required (retry the refund request)")
		if att.mode != refundModeReconcile {
			h.storeAcceptedRefund(r, &log, att)
		}
		common.JSONError(w, http.StatusInternalServerError, "REFUND_RECORD_FAILED",
			"refund was accepted by the payment gateway but could not be recorded; retry the same request to complete it",
			map[string]any{"refundId": cart.UUIDString(att.refundID), "providerRef": att.providerRef})
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"id":               cart.UUIDString(att.refundID),
		"returnId":         cart.UUIDString(att.returnID),
		"orderId":          cart.UUIDString(att.orderID),
		"provider":         att.provider,
		"providerRef":      att.providerRef,
		"amount":           att.amount,
		"status":           att.status,
		"refundedAmount":   outcome.refundedAmount,
		"refundableAmount": outcome.refundableAmount,
		"paymentStatus":    outcome.paymentStatus,
		"createdAt":        outcome.createdAt,
	}})
}

// reserveRefund is Phase A. The refunds row is examined before any status check so
// an accepted-but-unrecorded refund is always reconciled.
func (h *Handler) reserveRefund(ctx context.Context, tenantID, returnID pgtype.UUID, input refundInput) (refundAttempt, *apiError) {
	att := refundAttempt{tenantID: tenantID, returnID: returnID}
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return att, internalError("failed to start refund")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var returnStatus string
	if err := tx.QueryRow(ctx, `SELECT order_id, status FROM returns WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, returnID, tenantID).Scan(&att.orderID, &returnStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return att, &apiError{status: 404, code: "NOT_FOUND", message: "return not found"}
		}
		return att, internalError("failed to load return")
	}

	var (
		hasRow, settled, providerStatusNull, recent  bool
		rowStatus, rowProviderRef, rowProviderStatus string
		rowAmount                                    int64
		rowAttempt                                   int32
		rowPaymentID                                 pgtype.UUID
		rowProvider, rowReason                       string
	)
	err = tx.QueryRow(ctx, `SELECT id, status, provider_ref, COALESCE(provider_status,''), provider_status IS NULL,
	        settled_at IS NOT NULL, updated_at > now() - make_interval(secs => $3), amount, attempt, payment_id, provider, COALESCE(reason,'')
	   FROM refunds WHERE return_id=$1 AND tenant_id=$2 FOR UPDATE`, returnID, tenantID, refundInFlightSeconds).
		Scan(&att.refundID, &rowStatus, &rowProviderRef, &rowProviderStatus, &providerStatusNull, &settled, &recent, &rowAmount, &rowAttempt, &rowPaymentID, &rowProvider, &rowReason)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		att.mode = refundModeFresh
	case err != nil:
		return att, internalError("failed to load refund")
	default:
		hasRow = true
	}

	if hasRow {
		switch {
		case settled:
			return att, &apiError{status: 409, code: "REFUND_ALREADY_ISSUED", message: "this return has already been refunded"}
		case rowProviderRef != "":
			// The gateway accepted this refund; only local records are missing.
			att.mode = refundModeReconcile
			att.paymentID = rowPaymentID
			att.provider = rowProvider
			att.amount = rowAmount
			att.attempt = rowAttempt
			att.reason = rowReason
			att.providerRef = rowProviderRef
			att.providerStatus = rowProviderStatus
			att.status = rowStatus
			if att.status != payment.RefundStatusSucceeded && att.status != payment.RefundStatusPending {
				att.status = payment.RefundStatusSucceeded
			}
			if err := tx.Commit(ctx); err != nil {
				return att, internalError("failed to start refund")
			}
			return att, nil
		case rowStatus == "PENDING" && providerStatusNull && recent:
			return att, &apiError{status: 409, code: "REFUND_IN_PROGRESS", message: "a refund for this return is already in progress; retry in a minute",
				details: map[string]any{"refundId": cart.UUIDString(att.refundID)}}
		case rowStatus == "PENDING":
			// Unknown outcome (UNKNOWN marker, or a stale reservation from a dead
			// request): the same key, amount and reason must be sent again.
			att.mode = refundModeRetrySameKey
			att.attempt = rowAttempt
			att.amount = rowAmount
			att.reason = rowReason
		case rowStatus == "FAILED":
			att.mode = refundModeNewAttempt
			att.attempt = rowAttempt + 1
		default:
			return att, &apiError{status: 409, code: "REFUND_IN_PROGRESS", message: "refund state requires reconciliation by an operator",
				details: map[string]any{"refundId": cart.UUIDString(att.refundID)}}
		}
	}

	switch returnStatus {
	case "REFUNDED":
		return att, &apiError{status: 409, code: "REFUND_ALREADY_ISSUED", message: "this return has already been refunded"}
	case "APPROVED", "RECEIVED":
	default:
		return att, &apiError{status: 409, code: "REFUND_NOT_ALLOWED", message: "return must be approved or received"}
	}

	var paid, refunded int64
	paymentQuery := `SELECT p.id, COALESCE(p.provider,''), COALESCE(p.intent_token,''), COALESCE(p.amount, o.pricing_total), p.refunded_amount
	   FROM payments p JOIN orders o ON o.id=p.order_id AND o.tenant_id=p.tenant_id
	  WHERE p.order_id=$1 AND p.tenant_id=$2 AND p.status IN ('PAID','PARTIALLY_REFUNDED')
	  ORDER BY p.created_at DESC LIMIT 1 FOR UPDATE OF p`
	paymentArgs := []any{att.orderID, tenantID}
	if hasRow && rowPaymentID.Valid {
		paymentQuery = `SELECT p.id, COALESCE(p.provider,''), COALESCE(p.intent_token,''), COALESCE(p.amount, o.pricing_total), p.refunded_amount
		   FROM payments p JOIN orders o ON o.id=p.order_id AND o.tenant_id=p.tenant_id
		  WHERE p.id=$1 AND p.order_id=$2 AND p.tenant_id=$3 AND p.status IN ('PAID','PARTIALLY_REFUNDED')
		  FOR UPDATE OF p`
		paymentArgs = []any{rowPaymentID, att.orderID, tenantID}
	}
	if err := tx.QueryRow(ctx, paymentQuery, paymentArgs...).Scan(&att.paymentID, &att.provider, &att.paymentRef, &paid, &refunded); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return att, &apiError{status: 409, code: "PAYMENT_NOT_REFUNDABLE", message: "order has no paid payment to refund"}
		}
		return att, internalError("failed to load payment")
	}
	if att.mode == refundModeRetrySameKey && rowProvider != "" && !strings.EqualFold(rowProvider, att.provider) {
		return att, &apiError{status: 409, code: "REFUND_IN_PROGRESS", message: "refund state requires reconciliation by an operator",
			details: map[string]any{"refundId": cart.UUIDString(att.refundID)}}
	}

	gateway, exists := h.Providers[strings.ToLower(att.provider)]
	refundGateway, supports := gateway.(payment.RefundProvider)
	if !exists || !supports {
		return att, &apiError{status: 409, code: "REFUND_UNSUPPORTED", message: "payment provider does not support refunds"}
	}
	att.gateway = refundGateway

	refundable := paid - refunded
	if att.mode == refundModeRetrySameKey {
		// The key was already sent with this amount; resend it unchanged.
		if input.Amount != nil && *input.Amount != att.amount {
			return att, &apiError{status: 409, code: "REFUND_IN_PROGRESS",
				message: "a previous refund attempt for this return has an unknown outcome; retry with the same amount or without a body",
				details: map[string]any{"refundId": cart.UUIDString(att.refundID), "pendingAmount": att.amount}}
		}
	} else {
		att.amount = refundable
		if input.Amount != nil {
			att.amount = *input.Amount
		}
		att.reason = defaultRefundReason
		if input.Reason != nil && strings.TrimSpace(*input.Reason) != "" {
			att.reason = strings.TrimSpace(*input.Reason)
		}
	}
	if refundable <= 0 || att.amount > refundable {
		return att, &apiError{status: 409, code: "REFUND_EXCEEDS_REFUNDABLE", message: "refund amount exceeds the refundable amount",
			details: map[string]any{"requested": att.amount, "refundable": max(refundable, 0)}}
	}

	switch att.mode {
	case refundModeFresh:
		err = tx.QueryRow(ctx, `INSERT INTO refunds(tenant_id,return_id,order_id,payment_id,provider,provider_ref,amount,reason,status,attempt)
		    VALUES($1,$2,$3,$4,$5,'',$6,$7,'PENDING',0) RETURNING id, attempt`,
			tenantID, returnID, att.orderID, att.paymentID, att.provider, att.amount, att.reason).Scan(&att.refundID, &att.attempt)
	case refundModeNewAttempt:
		err = tx.QueryRow(ctx, `UPDATE refunds SET status='PENDING', attempt=attempt+1, amount=$3, reason=$4, provider=$5, payment_id=$6,
		        provider_status=NULL, failure_reason=NULL, updated_at=now()
		  WHERE id=$1 AND tenant_id=$2 AND status='FAILED' AND settled_at IS NULL RETURNING attempt`,
			att.refundID, tenantID, att.amount, att.reason, att.provider, att.paymentID).Scan(&att.attempt)
	case refundModeRetrySameKey:
		// provider_status=NULL + updated_at=now() is the in-flight marker, so a
		// concurrent retry gets REFUND_IN_PROGRESS instead of a parallel gateway call.
		// reason and amount are left untouched: the resend must be byte-identical.
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `UPDATE refunds SET provider_status=NULL, payment_id=$4, updated_at=now()
		  WHERE id=$1 AND tenant_id=$2 AND attempt=$3 AND status='PENDING' AND provider_ref='' AND settled_at IS NULL`,
			att.refundID, tenantID, att.attempt, att.paymentID)
		if err == nil && tag.RowsAffected() != 1 {
			err = errors.New("refund row changed concurrently")
		}
	}
	if err != nil {
		return att, internalError("failed to reserve refund")
	}
	if err := tx.Commit(ctx); err != nil {
		return att, internalError("failed to reserve refund")
	}
	return att, nil
}

// callRefundGateway is Phase B. It runs on a detached context with its own budget.
func (h *Handler) callRefundGateway(r *http.Request, att refundAttempt) (payment.RefundResult, error) {
	ctx, cancel := detachedContext(r, refundGatewayTimeout)
	defer cancel()
	req := payment.RefundRequest{
		OrderID:    cart.UUIDString(att.orderID),
		PaymentRef: att.paymentRef,
		RefundKey:  att.key(),
		Amount:     att.amount,
		Currency:   "IDR",
		Reason:     att.reason,
	}
	if att.mode == refundModeRetrySameKey {
		if lookup, ok := att.gateway.(payment.RefundLookupProvider); ok {
			res, found, err := lookup.LookupRefund(ctx, req)
			if err != nil {
				return payment.RefundResult{}, fmt.Errorf("refund lookup: %w", err)
			}
			if found {
				return res, nil
			}
		}
	}
	return att.gateway.Refund(ctx, req)
}

// recordGatewayFailure stores the Phase B outcome (the caller answers 502). Every update is
// guarded on the reserved attempt so a late reply cannot overwrite a row another
// request already moved on.
func (h *Handler) recordGatewayFailure(r *http.Request, log *zerolog.Logger, att refundAttempt, gwErr error) {
	ctx, cancel := detachedContext(r, refundWriteTimeout)
	defer cancel()
	definite := payment.IsDefiniteRefundFailure(gwErr)
	reason := truncateRunes(gwErr.Error(), 1000)
	var (
		tag pgconn.CommandTag
		err error
	)
	if definite {
		code := payment.RefundErrorCode(gwErr)
		if code == "" {
			code = "REJECTED"
		}
		tag, err = h.Pool.Exec(ctx, `UPDATE refunds SET status='FAILED', provider_status=$3, failure_reason=$4, updated_at=now()
		  WHERE id=$1 AND tenant_id=$2 AND attempt=$5 AND status='PENDING' AND provider_ref='' AND settled_at IS NULL`,
			att.refundID, att.tenantID, code, reason, att.attempt)
		log.Warn().Err(gwErr).Msg("refund rejected by gateway")
	} else {
		tag, err = h.Pool.Exec(ctx, `UPDATE refunds SET provider_status='UNKNOWN', failure_reason=$3, updated_at=now()
		  WHERE id=$1 AND tenant_id=$2 AND attempt=$4 AND status='PENDING' AND provider_ref='' AND settled_at IS NULL`,
			att.refundID, att.tenantID, reason, att.attempt)
		log.Warn().Err(gwErr).Msg("refund outcome unknown; the same gateway key will be checked and retried")
	}
	if err != nil {
		log.Error().Err(err).Bool("definite", definite).Msg("failed to record refund gateway failure")
	} else if tag.RowsAffected() != 1 {
		log.Warn().Bool("definite", definite).Msg("refund row changed concurrently; gateway failure not recorded")
	}
}

// storeAcceptedRefund best-effort persists the gateway acceptance when Phase C
// failed, so the retry reconciles instead of calling the gateway again.
func (h *Handler) storeAcceptedRefund(r *http.Request, log *zerolog.Logger, att refundAttempt) {
	ctx, cancel := detachedContext(r, refundWriteTimeout)
	defer cancel()
	tag, err := h.Pool.Exec(ctx, `UPDATE refunds SET provider_ref=$3, provider_status=$4, status=$5, updated_at=now()
	  WHERE id=$1 AND tenant_id=$2 AND attempt=$6 AND settled_at IS NULL AND provider_ref IN ('', $3)`,
		att.refundID, att.tenantID, att.providerRef, att.providerStatus, att.status, att.attempt)
	if err != nil || tag.RowsAffected() != 1 {
		log.Error().Err(err).Str("provider_ref", att.providerRef).
			Msg("failed to store gateway-accepted refund; a retry may call the gateway again with the same key")
	}
}

// finalizeRefund is Phase C: refunds, returns and payments change together. The
// payment is read by refunds.payment_id without a status filter, so neither a
// webhook nor an operator edit can strand the reconcile.
func (h *Handler) finalizeRefund(r *http.Request, att refundAttempt) (refundOutcome, error) {
	var out refundOutcome
	ctx, cancel := detachedContext(r, refundWriteTimeout)
	defer cancel()
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return out, fmt.Errorf("begin finalise: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var paid int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(p.amount, o.pricing_total) FROM payments p
	    JOIN orders o ON o.id=p.order_id AND o.tenant_id=p.tenant_id
	   WHERE p.id=$1 AND p.tenant_id=$2 FOR UPDATE OF p`, att.paymentID, att.tenantID).Scan(&paid); err != nil {
		return out, fmt.Errorf("load payment: %w", err)
	}
	if err := tx.QueryRow(ctx, `UPDATE refunds SET provider_ref=$3, provider_status=$4, status=$5, settled_at=now(), updated_at=now()
	   WHERE id=$1 AND tenant_id=$2 AND attempt=$6 AND settled_at IS NULL RETURNING created_at`,
		att.refundID, att.tenantID, att.providerRef, att.providerStatus, att.status, att.attempt).Scan(&out.createdAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) && att.mode == refundModeReconcile {
			var settled bool
			if lookupErr := h.Pool.QueryRow(ctx, `SELECT settled_at IS NOT NULL FROM refunds WHERE id=$1 AND tenant_id=$2`, att.refundID, att.tenantID).Scan(&settled); lookupErr == nil && settled {
				return out, errRefundAlreadySettled
			}
		}
		return out, fmt.Errorf("settle refund row: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE returns SET status='REFUNDED', updated_at=now() WHERE id=$1 AND tenant_id=$2`, att.returnID, att.tenantID)
	if err != nil {
		return out, fmt.Errorf("update return: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return out, errors.New("update return: return not found")
	}
	if err := tx.QueryRow(ctx, `UPDATE payments SET refunded_amount = refunded_amount + $3,
	        status = (CASE WHEN refunded_amount + $3 >= $4 THEN 'REFUNDED' ELSE 'PARTIALLY_REFUNDED' END)::payment_status,
	        updated_at = now()
	   WHERE id=$1 AND tenant_id=$2 AND refunded_amount + $3 <= $4
	   RETURNING status::text, refunded_amount`, att.paymentID, att.tenantID, att.amount, paid).Scan(&out.paymentStatus, &out.refundedAmount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, errors.New("update payment: refund exceeds paid amount")
		}
		return out, fmt.Errorf("update payment: %w", err)
	}
	payload, err := json.Marshal(map[string]any{
		"refundId":    cart.UUIDString(att.refundID),
		"returnId":    cart.UUIDString(att.returnID),
		"providerRef": att.providerRef,
		"amount":      att.amount,
		"attempt":     att.attempt,
		"status":      att.status,
	})
	if err != nil {
		return out, fmt.Errorf("encode payment event: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO payment_events(payment_id,status,payload) VALUES($1,$2::payment_status,$3)`, att.paymentID, out.paymentStatus, payload); err != nil {
		return out, fmt.Errorf("insert payment event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit finalise: %w", err)
	}
	out.refundableAmount = max(paid-out.refundedAmount, 0)
	return out, nil
}

// AdminReturnStatus changes a return's status. It is refused while the return has a
// refund in flight or accepted-but-unrecorded, and once the return is REFUNDED.
func (h *Handler) AdminReturnStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, _, ok := h.identity(w, r)
	if !ok {
		return
	}
	id, err := cart.ToUUID(chi.URLParam(r, "returnId"))
	if err != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid return id", nil)
		return
	}
	var input statusInput
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		common.JSONError(w, 400, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	status := strings.ToUpper(strings.TrimSpace(input.Status))
	allowed := map[string]bool{"APPROVED": true, "REJECTED": true, "RECEIVED": true, "CANCELLED": true}
	if !allowed[status] {
		common.JSONError(w, 400, "BAD_REQUEST", "unsupported return status", nil)
		return
	}
	ctx := r.Context()
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to update return", nil)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current string
	var refundID pgtype.UUID
	var refundStatus pgtype.Text
	var refundUnsettled bool
	err = tx.QueryRow(ctx, `SELECT r.status, f.id, f.status, (f.id IS NOT NULL AND f.settled_at IS NULL AND f.status <> 'FAILED')
	    FROM returns r LEFT JOIN refunds f ON f.return_id=r.id AND f.tenant_id=r.tenant_id
	   WHERE r.id=$1 AND r.tenant_id=$2 FOR UPDATE OF r`, id, tenantID).Scan(&current, &refundID, &refundStatus, &refundUnsettled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, 404, "NOT_FOUND", "return not found", nil)
		} else {
			common.JSONError(w, 500, "INTERNAL", "failed to update return", nil)
		}
		return
	}
	if current == "REFUNDED" {
		common.JSONError(w, 409, "REFUND_ALREADY_ISSUED", "this return has been refunded; its status can no longer change", nil)
		return
	}
	if refundUnsettled {
		common.JSONError(w, 409, "RETURN_REFUND_PENDING", "this return has a refund in progress or awaiting reconciliation; retry the refund first",
			map[string]any{"refundId": cart.UUIDString(refundID), "refundStatus": refundStatus.String})
		return
	}
	var updated time.Time
	if err := tx.QueryRow(ctx, `UPDATE returns SET status=$1,updated_at=now() WHERE id=$2 AND tenant_id=$3 RETURNING updated_at`, status, id, tenantID).Scan(&updated); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to update return", nil)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		common.JSONError(w, 500, "INTERNAL", "failed to update return", nil)
		return
	}
	common.JSON(w, 200, map[string]any{"data": map[string]any{"id": cart.UUIDString(id), "status": status, "updatedAt": updated}})
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// refundSummaryColumns selects the refunds row joined as "f" onto a return.
const refundSummaryColumns = `f.id, f.status, f.amount, f.provider_ref, f.provider_status, f.attempt, f.settled_at IS NOT NULL, f.created_at`

type refundSummaryRow struct {
	id             pgtype.UUID
	status         pgtype.Text
	amount         pgtype.Int8
	providerRef    pgtype.Text
	providerStatus pgtype.Text
	attempt        pgtype.Int4
	settled        pgtype.Bool
	createdAt      pgtype.Timestamptz
}

func (f *refundSummaryRow) dest() []any {
	return []any{&f.id, &f.status, &f.amount, &f.providerRef, &f.providerStatus, &f.attempt, &f.settled, &f.createdAt}
}

func (f refundSummaryRow) customerView() map[string]any {
	if !f.id.Valid {
		return nil
	}
	return map[string]any{
		"id":          cart.UUIDString(f.id),
		"status":      f.status.String,
		"amount":      f.amount.Int64,
		"providerRef": f.providerRef.String,
		"settled":     f.settled.Bool,
		"createdAt":   f.createdAt.Time,
	}
}

// adminView adds gateway diagnostics that are not shown to customers.
func (f refundSummaryRow) adminView() map[string]any {
	view := f.customerView()
	if view == nil {
		return nil
	}
	view["providerStatus"] = nullableText(f.providerStatus)
	view["attempt"] = f.attempt.Int32
	return view
}
