# Webhook Endpoints

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

**Response:** `200 OK`

On success the backend updates the order payment status and may trigger voucher settlement. The response body is empty.

**Common Error Response:**

```json
{
  "error": {
    "code": "INVALID_SIGNATURE",
    "message": "signature verification failed"
  }
}
```

Common error codes: `PAYMENT_NOT_CONFIGURED`, `PROVIDER_NOT_SUPPORTED`, `INVALID_BODY`, `INVALID_SIGNATURE`, `REPLAY`, `INVALID_ORDER_ID`, `AMOUNT_MISMATCH`, `PAYMENT_NOT_FOUND`, `PAYMENT_UPDATE_ERROR`, `INTERNAL`.

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
