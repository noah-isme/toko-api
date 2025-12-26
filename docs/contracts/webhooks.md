# Webhook Endpoints

## Payment Notification Webhook

```http
POST /api/v1/webhooks/payment/midtrans
Content-Type: application/json
X-Signature: <hmac-signature>
```

**Request dari Payment Gateway:**
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
