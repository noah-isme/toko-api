# Checkout Endpoints

## 4.1 Create Order (Checkout)

```http
POST /api/v1/checkout
Content-Type: application/json
Authorization: Bearer <token>
```

**Request:**
```json
{
  "cartId": "cart-uuid",
  "shippingAddressId": "address-uuid",
  "shippingService": "jne-reg",
  "shippingCost": 15000,
  "paymentMethod": "bank_transfer",
  "notes": "Please call before delivery"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `cartId` | string (uuid) | Yes | ID of the cart to check out |
| `shippingAddressId` | string (uuid) | Yes | ID of the user's saved shipping address. Must belong to the authenticated user. |
| `shippingService` | string | Yes | Shipping service code (e.g. `jne-reg`). Passed through from the shipping quote. |
| `shippingCost` | integer | Yes | Shipping cost in IDR (smallest currency unit). Must be ≥ 0. |
| `paymentMethod` | string (enum) | Yes | One of the payment methods listed below. |
| `notes` | string | No | Optional order notes (max 500 chars). |

**Response:** `201 Created`
```json
{
  "data": {
    "orderId": "order-uuid",
    "orderNumber": "ORD-20251207-001",
    "status": "pending_payment",
    "total": 21135000,
    "currency": "IDR",
    "paymentMethod": "bank_transfer",
    "paymentUrl": "https://payment.gateway.com/pay/xxx",
    "paymentExpiry": "2025-12-08T10:00:00Z",
    "createdAt": "2025-12-07T10:00:00Z"
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `orderId` | string (uuid) | Internal order ID |
| `orderNumber` | string | Human-readable order number, format `ORD-YYYYMMDD-NNN` (see below) |
| `status` | string | Order status (lowercase, see status flow) |
| `total` | integer | Total amount in IDR (subtotal - discount + tax + shipping) |
| `currency` | string | Currency code, always `IDR` |
| `paymentMethod` | string | Echo of the request `paymentMethod` |
| `paymentUrl` | string | Payment gateway redirect URL. May be empty string if the payment provider is not configured. |
| `paymentExpiry` | string (date-time) \| null | When the payment intent expires (typically 15 min). `null` if no intent was created. |
| `createdAt` | string (date-time) | Order creation timestamp |

### Order Number Format

`orderNumber` follows the pattern `ORD-YYYYMMDD-NNN`:
- `YYYYMMDD` — the UTC date of order creation
- `NNN` — a zero-padded sequence number starting at `001`, incremented per order within the same UTC day

Example: the first order on 2025-12-07 is `ORD-20251207-001`, the second is `ORD-20251207-002`.

### Payment Methods

- `bank_transfer` - Bank Transfer
- `virtual_account` - Virtual Account
- `credit_card` - Credit Card
- `ewallet_gopay` - GoPay
- `ewallet_ovo` - OVO
- `ewallet_dana` - DANA

Requests with a `paymentMethod` not in this list are rejected with `400 BAD_REQUEST`.

### Payment Provider Behavior

On successful checkout, the backend automatically creates a payment intent via the configured provider (Midtrans or Xendit):
- `paymentUrl` — the provider's redirect URL for the customer to complete payment
- `paymentExpiry` — when the intent expires (default 15 minutes)

If no payment provider is configured or intent creation fails, the order is still created with status `pending_payment`. In this case `paymentUrl` is an empty string and `paymentExpiry` is `null`. The customer can retry payment via `POST /api/v1/payments/intent`.

### Order Status Flow
```
pending_payment → paid → packed → shipped → out_for_delivery → delivered
                   ↓
                cancelled
```

Status values are returned in **lowercase** (e.g. `pending_payment`, not `PENDING_PAYMENT`).

### Error Cases

| HTTP | Code | Description |
|------|------|-------------|
| 400 | `BAD_REQUEST` | Missing or invalid field (cartId, shippingAddressId, shippingService, paymentMethod) |
| 400 | `BAD_REQUEST` | `paymentMethod` is not in the allowed enum |
| 400 | `BAD_REQUEST` | `shippingCost` is negative |
| 400 | `BAD_REQUEST` | `cartId` or `shippingAddressId` is not a valid UUID |
| 401 | `UNAUTHORIZED` | Missing or invalid auth token |
| 500 | `INTERNAL` | `shippingAddressId` does not exist or belongs to another user |
| 500 | `INTERNAL` | Cart is empty or does not belong to the user |
| 500 | `INTERNAL` | Tenant context missing |
