# Webhook Endpoints

> **Last Updated:** 2026-10-25

Webhooks are public endpoints invoked by external payment gateways and shipping couriers. They do **not** require a `Bearer` token; authentication is provider-specific (signature verification, API keys, etc.).

---

## Payment Notification Webhook

```http
POST /api/v1/webhooks/payment/{provider}
Content-Type: application/json
X-Signature: <provider-signature>
```

The `{provider}` path parameter is the configured payment provider key (e.g. `midtrans`, `xendit`). The request body and signature header are provider-specific.

**Example (Midtrans):**

```json
{
  "transaction_id": "xxx",
  "order_id": "ORD-20251207-001",
  "payment_type": "bank_transfer",
  "transaction_status": "settlement",
  "gross_amount": "21135000"
}
```

**Response:** `204 No Content`

Processing rules:

- The settlement transaction locks the order row first, so it serialises with the checkout-expiry
  job, customer cancel, admin payment confirmation and intent creation.
- Payment rows and `payment_events` change only on a real state transition: `PAID` is applied
  unless the payment is already `PAID`/`REFUNDED`; `FAILED`/`EXPIRED` only replace `PENDING`;
  `PENDING`/`REFUNDED` callbacks never overwrite anything. A callback that changes nothing returns
  `204` and emits nothing, so retries and duplicate callbacks (Midtrans `capture` + `settlement`)
  are idempotent.
- `PAID` for a `PENDING_PAYMENT` order: order `PAID` through the status guard, reservations and
  flash-sale quota committed, voucher settled, `order.paid` emitted once.
- `PAID` for an `EXPIRED`/`CANCELLED` order: late-payment path (payment.md 6.5), `payment.late`.
- `EXPIRED` for a pending order: order `EXPIRED`, stock released, `payment.expired` +
  `order.canceled`. `FAILED`: order `CANCELLED`, stock released, `payment.failed` + `order.canceled`.
  Stock is released only when this callback terminated the order.
- Replay protection: the key `wh:<provider>:sha256(body)` is kept only when the request succeeded
  (2xx). After a failed request (4xx/5xx other than `409 REPLAY`) the provider's retry with the same
  body is processed again.

**Common Error Response:**

```json
{
  "error": {
    "code": "INVALID_SIGNATURE",
    "message": "signature verification failed"
  }
}
```

Common error codes: `PAYMENT_NOT_CONFIGURED`, `PROVIDER_NOT_SUPPORTED`, `INVALID_BODY`, `WEBHOOK_INVALID`, `INVALID_SIGNATURE`, `REPLAY`, `REPLAY_STORE_ERROR`, `INVALID_ORDER_ID`, `ORDER_NOT_FOUND`, `PAYMENT_NOT_FOUND`, `AMOUNT_MISMATCH`, `SETTLEMENT_ERROR`.

### Outbound webhooks (domain events)

Domain events are delivered only to the active webhook endpoints of the **event's tenant** (the
order's tenant). Events emitted without a tenant are not delivered (logged as a warning). Topics:
`order.created`, `order.paid`, `order.canceled`, `payment.failed`, `payment.expired`,
`payment.late` (ops only, see payment.md 6.5), `payment.proof_unreviewed` (ops only, see
payment.md 6.4), `shipment.shipped`, `shipment.out_for_delivery`,
`shipment.delivered`. Events are emitted only on real state changes.

---

## Shipping Courier Webhook

```http
POST /api/v1/webhooks/shipping/{courier}
Content-Type: application/json
```

The `{courier}` path parameter identifies the shipping provider (e.g. `jne`, `sicepat`). The payload can be sent as JSON or as query parameters; the handler accepts both and merges them.

**Request body (JSON):**

```json
{
  "orderId": "order-uuid",
  "trackingNumber": "JP1234567890",
  "externalStatus": "in_transit",
  "description": "Paket telah tiba di transit hub Jakarta",
  "location": "Jakarta",
  "occurredAt": "2025-12-07T10:00:00Z"
}
```

**Query parameter alternative:**

```http
POST /api/v1/webhooks/shipping/jne?orderId=order-uuid&tracking=JP1234567890&status=in_transit&description=Paket%20telah%20tiba&location=Jakarta&occurredAt=2025-12-07T10:00:00Z
```

**Fields:**

- `orderId` — required, order UUID or order number resolved by the service
- `trackingNumber` — optional, courier tracking number
- `externalStatus` / `status` — required, external courier status label
- `description` — optional, human-readable event description
- `location` — optional, event location
- `occurredAt` — optional, RFC3339 timestamp

**Recognised external status labels** (case-insensitive):

- `picked`, `pickup` → `PICKED`
- `shipped`, `in_transit`, `in-transit` → `SHIPPED`
- `out_for_delivery`, `out-for-delivery` → `OUT_FOR_DELIVERY`
- `delivered` → `DELIVERED`

Unknown statuses are rejected with `400 Bad Request`.

**Response:** `204 No Content`

On success the backend appends a shipment event and updates the shipment status. The response body is empty.

**Common Error Response:**

```json
{
  "error": {
    "code": "INVALID_STATE",
    "message": "shipment transition not allowed"
  }
}
```

Common error codes: `INTERNAL`, `BAD_REQUEST`, `REPLAY`, `NOT_FOUND`, `INVALID_STATE`, `INTERNAL`.

---

## Shipment tracking poller (worker)

Besides courier webhooks, the worker's `shipment_tracking` job polls the carrier (RajaOngkir
waybill) for shipments in `SHIPPED` / `OUT_FOR_DELIVERY`. It applies only forward changes
(`SHIPPED` → `OUT_FOR_DELIVERY` → `DELIVERED`) through the same service path as this webhook, so
shipment events, order status, customer email and `shipment.*` events behave identically. Polled
events have `occurredAt` = carrier time (WIB converted to UTC), `description`/`location` from the
carrier and `rawPayload` `{source: "tracking_poll", status, description, location, occurredAt}`.
`DELIVERED` is taken only from the carrier's delivered flag / delivery status, never from manifest
text. Events older than `SHIPMENT_TRACK_SILENT_AFTER_HOURS` (default 48 h) are recorded without
customer notifications or domain events.

