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

**Payment Methods:**
- `bank_transfer` - Bank Transfer
- `virtual_account` - Virtual Account
- `credit_card` - Credit Card
- `ewallet_gopay` - GoPay
- `ewallet_ovo` - OVO
- `ewallet_dana` - DANA

**Order Status Flow:**
```
pending_payment → paid → processing → shipped → delivered
                   ↓
                cancelled
```
