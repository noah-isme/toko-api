# Admin Endpoints

## 6.1 Create Voucher

```http
POST /api/v1/admin/vouchers
Content-Type: application/json
Authorization: Bearer <admin_token>
```

**Request:**
```json
{
  "code": "DISC20",
  "type": "percentage",
  "value": 20,
  "minSpend": 100000,
  "maxDiscount": 50000,
  "usageLimit": 100,
  "perUserLimit": 1,
  "validFrom": "2025-12-01T00:00:00Z",
  "validUntil": "2025-12-31T23:59:59Z",
  "description": "20% discount up to 50k"
}
```

**Voucher Types:**
- `percentage`: Discount in percentage (value: 1-100)
- `fixed`: Fixed amount discount

**Response:** `201 Created`

---

## 6.2 Update Order Status

```http
PATCH /api/v1/admin/orders/{orderId}/status
Content-Type: application/json
Authorization: Bearer <admin_token>
```

**Request:**
```json
{
  "status": "processing"
}
```

**Valid Status Transitions:**
- `pending_payment` → `paid`, `cancelled`
- `paid` → `processing`, `cancelled`
- `processing` → `shipped`
- `shipped` → `delivered`

**Response:** `200 OK`

---

## 6.3 Create Shipment

```http
POST /api/v1/admin/orders/{orderId}/shipment
Content-Type: application/json
Authorization: Bearer <admin_token>
```

**Request:**
```json
{
  "courier": "jne",
  "service": "REG",
  "trackingNumber": "JP1234567890",
  "estimatedDelivery": "2-3 hari"
}
```

**Response:** `201 Created`
