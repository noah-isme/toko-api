# Payment Endpoints

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

Common error codes: `UNAUTHENTICATED`, `BAD_REQUEST`, `ORDER_NOT_FOUND`, `INTENT_FAILED`.

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
- `EXPIRED` — payment intent expired
- `REFUNDED` — payment refunded

**Error Response:**

```json
{
  "error": {
    "code": "STATUS_ERROR",
    "message": "..."
  }
}
```

Common error codes: `UNAUTHENTICATED`, `BAD_REQUEST`, `ORDER_NOT_FOUND`, `STATUS_ERROR`.
