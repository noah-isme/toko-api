# Payment Endpoints

> **Last Updated:** 2026-10-25

## 6.1 Create or Reuse Payment Intent

```http
POST /api/v1/payments/intent
Content-Type: application/json
Authorization: Bearer <token>
```

**Request:**

```json
{
  "orderId": "order-uuid",
  "channel": "snap"
}
```

- `orderId` is required and must be a valid order belonging to the authenticated user.
- `channel` is optional and is passed to the configured payment provider.

**Response:** `200 OK`

```json
{
  "data": {
    "provider": "midtrans",
    "token": "snap-token-123",
    "redirectUrl": "https://payment.example.com/pay/xxx",
    "expiresAt": "2025-12-08T10:00:00Z"
  }
}
```

- `provider` is always present (e.g. `midtrans`, `xendit`, or `unknown`).
- `token`, `redirectUrl`, and `expiresAt` are provider-dependent and may be omitted when not available.

**Error Response:**

```json
{
  "error": {
    "code": "INTENT_FAILED",
    "message": "..."
  }
}
```

Common error codes: `UNAUTHORIZED`, `BAD_REQUEST`, `NOT_FOUND`, `INTENT_FAILED`, `INVALID_STATE`.

- `409 INVALID_STATE` (`"order is no longer pending payment"`): the order is not `pending_payment`
  (it expired, was cancelled or was paid), including when that happened while the provider call
  was in flight. No payment link is returned; refresh the order.
- `expiresAt` is the provider's own deadline. Midtrans Snap transactions are created with an
  explicit expiry equal to `PAYMENT_INTENT_EXPIRES_MIN` (default 15 minutes, rounded up to whole
  minutes) for **every** method, including virtual account and bank transfer, so the customer
  cannot pay after the local deadline. This 15-minute VA/bank-transfer window is a deliberate
  product decision (it replaces Midtrans' ~24 h default); raise `PAYMENT_INTENT_EXPIRES_MIN` to
  give customers longer.
- While the order is still `pending_payment` (deadline + grace, see 6.4) a new call issues a fresh
  intent with a new deadline; the stock stays reserved.

---

## 6.2 Get Consolidated Payment Status

```http
GET /api/v1/payments/{orderId}/status
Authorization: Bearer <token>
```

**Response:** `200 OK`

```json
{
  "data": {
    "status": "PAID"
  }
}
```

**Status values:**

- `PENDING` — no successful payment yet
- `PAID` — payment settled
- `FAILED` — payment failed or order was cancelled
- `EXPIRED` — payment intent expired, or the order expired (order status `EXPIRED`)
- `PARTIALLY_REFUNDED` — part of the paid amount was refunded
- `REFUNDED` — the whole paid amount was refunded

Refund webhook notifications (Midtrans `refund` / `partial_refund`) are recorded as payment events
and do not change `status`; the admin refund flow (`POST /api/v1/admin/returns/{returnId}/refund`,
see [customer-operations.md](customer-operations.md#refunds)) owns `status` and the refunded amount.
Once a payment is `PARTIALLY_REFUNDED` or `REFUNDED`, every later webhook for it (for example a
re-delivered `settlement`) is likewise recorded as an event only: it never moves the payment back
to `PAID` or re-settles the order.

**Error Response:**

```json
{
  "error": {
    "code": "STATUS_ERROR",
    "message": "..."
  }
}
```

Common error codes: `UNAUTHORIZED`, `BAD_REQUEST`, `NOT_FOUND`, `STATUS_ERROR`.

## 6.3 Payment instructions and proof

- `GET /api/v1/payments/{orderId}/instructions` returns the configured method-specific steps, bank details, and QR URL.
- `POST /api/v1/payments/{orderId}/proof` accepts a multipart `proof` file up to 5 MB and stores it privately against the order.
  - `409 INVALID_STATE` (`"payment proof can only be uploaded for pending orders"`) when the order
    is not `pending_payment` (expired, cancelled or paid). Hide or disable the upload for those
    orders and handle the 409.
- An uploaded proof keeps the order from expiring for `JOB_CHECKOUT_PROOF_HOLD_HOURS` (default
  72 h) counted from the **first** proof (re-uploading does not extend it). An admin confirms the
  transfer with `POST /api/v1/admin/orders/{id}/confirm-payment` (see admin.md), which settles the
  order exactly like a provider `PAID` callback. If nobody confirms it within the hold, the order
  expires with the normal `payment.expired` / `order.canceled` notifications (never silently,
  however old) and the ops-only `payment.proof_unreviewed` event (6.4).

## 6.4 Order expiry (worker)

The worker's `checkout_expiry` job (every `JOB_CHECKOUT_EXPIRY_INTERVAL_SEC`, default 60 s) moves a
`PENDING_PAYMENT` order to the terminal status `EXPIRED` when its payment deadline plus a grace
period has passed:

- Deadline = the latest `expiresAt` over the order's payments, or
  `createdAt + PAYMENT_INTENT_EXPIRES_MIN` when the order has no payment. Midtrans intents created
  before this release were sent without a Snap expiry and stay payable at Midtrans for its default
  window, so while such an intent is `PENDING` its deadline is at least `createdAt + 24 h` (new
  intents record `response.ExpirySynced: true` in `provider_payload` and use `expiresAt`). Grace = `JOB_CHECKOUT_EXPIRY_GRACE_SEC` (default 600 s). Until then the
  order stays payable and its stock stays reserved.
- Orders inside the proof hold (6.3) are skipped.
- On expiry: every `PENDING` payment of the order becomes `EXPIRED` (with a `payment_events` row), stock and
  flash-sale quota are released, and `payment.expired` + `order.canceled` are emitted (customer
  email, in-app notification `payment_expired` / `order_canceled`, outbound webhooks) with payload
  `{orderId, orderNumber, tenantId, paymentId?, userId?, email?, status: "EXPIRED", reason: "payment_deadline", source: "worker"}`.
- Backlog rule: orders whose deadline + grace passed more than
  `JOB_CHECKOUT_EXPIRY_SILENT_AFTER_HOURS` (default 24 h) ago are expired **silently** (no events,
  no notifications); the `payment_events` row carries `"silent": true`. Orders with a payment
  proof are never expired silently.
- An order that expires with a payment proof nobody confirmed also emits the ops-only topic
  `payment.proof_unreviewed` `{orderId, orderNumber?, tenantId, userId?, status: "EXPIRED",
  firstProofAt, proofCount, source: "worker"}` (worker warn log of the same name; counter
  `orders_expired_proof_unreviewed`). Like `payment.late` it carries no email and never produces a
  customer email or in-app notification; it is delivered to the tenant's outbound webhooks.
- A provider `expire` callback for a pending order has the same effect (order `EXPIRED`, not
  `CANCELLED`); a provider `deny`/`cancel` callback still yields `CANCELLED`.
- `EXPIRED` is terminal: the customer cannot cancel it, admins cannot move it, and a new intent or
  proof upload returns `409 INVALID_STATE`.

## 6.5 Late payments

A `PAID` callback for an order that is already `EXPIRED` or `CANCELLED` is recorded but does not
revive the order (its stock may already be sold): the payment becomes `PAID`, the order keeps its
status, no stock moves, no voucher is settled and no `order.paid` is emitted. The webhook answers
`204`, increments `payment_late_total{provider}`, logs a warning, and emits the ops-only topic
`payment.late` `{orderId, paymentId, tenantId, status: "PAID", orderStatus, userId?}`. It carries no
email and never produces a customer email or in-app notification. Ops refund the payment by hand
(RUNBOOK "Late payments"). In the admin order detail such an order shows `paymentStatus: "PAID"`
on an `EXPIRED`/`CANCELLED` order.

