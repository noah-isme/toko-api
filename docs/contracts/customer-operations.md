# Returns & Support

> **Last Updated:** 2026-10-25

Customer endpoints require authentication and tenant membership:

- `POST /api/v1/orders/{orderId}/returns`
- `GET /api/v1/returns` and `GET /api/v1/returns/{returnId}`
- `GET/POST /api/v1/support/tickets`
- `GET /api/v1/support/tickets/{ticketId}/messages`
- `POST /api/v1/support/tickets/{ticketId}/messages`

Admin workflows are available under `/api/v1/admin/returns` and `/api/v1/admin/support/tickets`, including status updates, refunds, message history, and agent messages. Both customer and admin ticket message endpoints return chronological transcripts.

## Idempotency-Key

`POST /api/v1/orders/{orderId}/cancel`, `POST /api/v1/orders/{orderId}/returns` and
`POST /api/v1/admin/returns/{returnId}/refund` accept an optional `Idempotency-Key` header
(same middleware as carts, checkout and payment intent).

- No header: the request is processed as before.
- Within `IDEMPOTENCY_TTL_SEC` (default 600 s) a repeated key answers `409 IDEMPOTENT_REPLAY`
  without running the handler.
- **Exception:** a 5xx response releases the key immediately, so the same key can be reused to
  retry a failed request (this applies to every route using the middleware). A handler panic,
  which ends as a 500, releases it too.
- Keys are global, not scoped per user or route: generate a fresh key (e.g. a UUID) per logical
  operation and reuse it only to retry that operation.

## Returns

Each return in `GET /api/v1/admin/returns` and `GET /api/v1/returns/{returnId}` has a `refund`
field, `null` when the return has no refund:

```json
"refund": {
  "id": "<uuid>",
  "status": "SUCCEEDED",
  "amount": 40000,
  "providerRef": "rfd-...",
  "settled": true,
  "createdAt": "2026-10-18T10:00:00Z",
  "providerStatus": "SUCCEEDED",
  "attempt": 0
}
```

- `status`: `PENDING` | `SUCCEEDED` | `FAILED`.
- `settled`: only `true` means local records were finalised (money moved). `amount` may be less
  than the order total (partial refund).
- `PENDING` + `settled: false`: a gateway call is in flight or its outcome is unknown; retry the refund.
- `FAILED`: the gateway rejected the last attempt; a retry uses a new gateway key.
- `providerStatus` (nullable) and `attempt` appear on the admin list only. `providerStatus` is
  `UNKNOWN` when the last gateway call had no definite outcome, otherwise the gateway status or
  rejection code. `attempt` counts gateway keys used before the current one (starts at 0).

### `PATCH /api/v1/admin/returns/{returnId}`

Body `{"status": "APPROVED" | "REJECTED" | "RECEIVED" | "CANCELLED"}`. New refusals:

| HTTP | code | when |
|---|---|---|
| 409 | `RETURN_REFUND_PENDING` | the return has a refund in flight, with an unknown outcome, or accepted by the gateway but not recorded. Details `{refundId, refundStatus}`. Retry the refund first. |
| 409 | `REFUND_ALREADY_ISSUED` | the return is `REFUNDED`; its status can no longer change. |

A `FAILED` refund does not block status changes.

## Refunds

### `POST /api/v1/admin/returns/{returnId}/refund`

Permission `refunds:write`, audit-logged. Headers: `Authorization: Bearer <token>`, optional `Idempotency-Key`.

Request body (optional, `Content-Type: application/json`):

```json
{ "amount": 40000, "reason": "damaged item" }
```

- `amount` (integer, IDR, optional): defaults to the remaining refundable amount of the order's
  payment. Must be `> 0` and `<= refundableAmount`.
- `reason` (string, optional, at most 500 characters): defaults to `customer return`. Sent to the
  gateway (Midtrans `reason`, Xendit `metadata.note`).
- No body (or `{}`) keeps the old behaviour: refund everything that is left.

One refund per return. An order can have several returns over time (one open at a time), so it
can be refunded in parts: the payment becomes `PARTIALLY_REFUNDED` while
`0 < refundedAmount < paid amount`, and `REFUNDED` when everything is refunded.

`200 OK`:

```json
{
  "data": {
    "id": "<refund uuid>",
    "returnId": "<uuid>",
    "orderId": "<uuid>",
    "provider": "xendit",
    "providerRef": "rfd-...",
    "amount": 40000,
    "status": "SUCCEEDED",
    "refundedAmount": 40000,
    "refundableAmount": 60000,
    "paymentStatus": "PARTIALLY_REFUNDED",
    "createdAt": "2026-10-18T10:00:00Z"
  }
}
```

- `status`: `SUCCEEDED` | `PENDING` (accepted by the gateway, settles asynchronously; local
  records are final either way).
- `paymentStatus`: `PARTIALLY_REFUNDED` | `REFUNDED`.
- `refundedAmount` is cumulative for the order's payment; `refundableAmount` is what is left.

Errors (`{"error": {"code", "message", "details"}}`):

| HTTP | code | when |
|---|---|---|
| 400 | `BAD_REQUEST` | invalid return id, malformed body, `amount <= 0`, `reason` over 500 characters |
| 404 | `NOT_FOUND` | return not in this tenant |
| 409 | `REFUND_NOT_ALLOWED` | return status is not `APPROVED`/`RECEIVED` |
| 409 | `REFUND_ALREADY_ISSUED` | this return already has a recorded refund |
| 409 | `PAYMENT_NOT_REFUNDABLE` | no `PAID`/`PARTIALLY_REFUNDED` payment for the order |
| 409 | `REFUND_EXCEEDS_REFUNDABLE` | `amount > refundableAmount`, or nothing left. Details `{requested, refundable}` |
| 409 | `REFUND_IN_PROGRESS` | another request for this return is calling the gateway (retry in about 60 s); or the previous attempt has an unknown outcome and the body `amount` differs from it (details `{refundId, pendingAmount}`: retry with that amount or without a body); or message "refund state requires reconciliation by an operator" (details `{refundId}`): the refunds row cannot be resolved by the API (payment provider changed since the unknown-outcome attempt, or no payment for an accepted refund); retrying does not help, an operator must reconcile it with the gateway |
| 409 | `REFUND_UNSUPPORTED` | the payment provider has no refund support |
| 409 | `IDEMPOTENT_REPLAY` | same `Idempotency-Key` within the TTL after a 2xx/4xx response |
| 502 | `REFUND_FAILED` | gateway rejected or did not answer; `message` carries the gateway error. Details `{refundId, retryable}` (see below) |
| 500 | `REFUND_RECORD_FAILED` | gateway accepted but local records were not finalised. Details `{refundId, providerRef}`. Retry the same request to complete it |
| 500 | `INTERNAL` | database error before the gateway was called |

`REFUND_FAILED.details.retryable`:

- `false`: the gateway definitively rejected the refund (e.g. Midtrans `412` "Merchant cannot
  modify the status of the transaction", Midtrans `202` denied, Xendit `INSUFFICIENT_BALANCE`).
  No money moved. Fix the cause, then retry; the retry uses a new gateway key.
- `true`: the outcome is unknown (timeout, gateway 5xx, a Midtrans body `status_code` of 5xx even
  under HTTP 200, Midtrans `201`, Xendit `409` duplicate or in-progress, or a failed lookup or local
  configuration error while retrying an unknown-outcome key). Retry as is. The retry first asks the gateway whether the previous key was
  applied (Midtrans transaction status `refunds[].refund_key`, Xendit refund list
  `reference_id`) and only then re-sends the **same** key with the same amount and reason, so
  money cannot move twice. The return stays locked (`RETURN_REFUND_PENDING`) until a retry
  resolves it.

**Retry rules.** The route is safe to retry. After any 5xx the same `Idempotency-Key` may be
reused (5xx releases it). `REFUND_RECORD_FAILED` is completed by a retry without a second
gateway call (reconcile). After a 409 the key stays locked for the TTL.

**Gateway keys.** Each attempt uses `rf-<refundId>-<attempt>`: Midtrans `refund_key`, Xendit
`idempotency-key` header and `reference_id`. Xendit refunds address the payment by
`payment_request_id` when the stored payment token starts with `pr-`, otherwise by `invoice_id`
(this integration stores the Xendit invoice id).

**Webhooks.** Midtrans `refund` / `partial_refund` notifications are recorded as payment events
and do not change the payment status; the admin refund flow is the only writer of
`payments.status` (refund values) and the refunded amount.

**Data semantics for reporting.** A `refunds` row is written as `PENDING` before the gateway is
called. Only rows with `settled_at IS NOT NULL` represent money that moved, and `amount` may be a
partial amount. Revenue reports must not exclude a whole order just because a non-`FAILED`
refunds row exists.
