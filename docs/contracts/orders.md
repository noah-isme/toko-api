# Order Management Endpoints

> **Last Updated:** 2026-08-07

## 4.2 List User Orders

```http
GET /api/v1/orders?page=1&limit=20
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "order-uuid",
      "orderNumber": "ORD-20251207-001",
      "status": "shipped",
      "statusLabel": "Sedang Dikirim",
      "total": 21135000,
      "currency": "IDR",
      "itemCount": 2,
      "thumbnailUrl": "https://cdn.toko.com/products/s24.jpg",
      "paymentMethod": "bank_transfer",
      "createdAt": "2025-12-07T10:00:00Z",
      "updatedAt": "2025-12-07T12:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 5
  }
}
```

---

## 4.3 Get Order Detail

```http
GET /api/v1/orders/{orderId}
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "id": "order-uuid",
    "orderNumber": "ORD-20251207-001",
    "status": "shipped",
    "statusLabel": "Sedang Dikirim",
    "user": {
      "id": "user-uuid",
      "name": "John Doe",
      "email": "john@example.com"
    },
    "items": [
      {
        "id": "item-uuid",
        "productId": "product-uuid",
        "productTitle": "Samsung Galaxy S24",
        "productSlug": "samsung-galaxy-s24",
        "variantName": "8GB/128GB - Black",
        "qty": 2,
        "unitPrice": 12000000,
        "subtotal": 24000000,
        "imageUrl": "https://cdn.toko.com/products/s24.jpg"
      }
    ],
    "shippingAddress": {
      "receiverName": "John Doe",
      "phone": "+6281234567890",
      "addressLine1": "Jl. Sudirman No. 123",
      "addressLine2": "Apt 45B",
      "city": "Jakarta Selatan",
      "province": "DKI Jakarta",
      "postalCode": "12190",
      "country": "Indonesia"
    },
    "pricing": {
      "subtotal": 24000000,
      "discount": 4800000,
      "tax": 1920000,
      "shipping": 15000,
      "total": 21135000
    },
    "voucher": {
      "code": "DISC20",
      "discount": 4800000
    },
    "shipping": {
      "courier": "JNE",
      "service": "REG",
      "trackingNumber": "JP1234567890",
      "estimatedDelivery": "2-3 hari",
      "shippedAt": "2025-12-07T14:00:00Z"
    },
    "payment": {
      "method": "bank_transfer",
      "methodLabel": "Transfer Bank",
      "status": "paid",
      "paidAt": "2025-12-07T11:30:00Z",
      "paymentUrl": "https://payment.gateway.com/pay/xxx",
      "expiryAt": "2025-12-08T10:00:00Z"
    },
    "notes": "Please call before delivery",
    "currency": "IDR",
    "createdAt": "2025-12-07T10:00:00Z",
    "updatedAt": "2025-12-07T14:00:00Z",
    "statusHistory": [
      {
        "status": "pending_payment",
        "timestamp": "2025-12-07T10:00:00Z"
      },
      {
        "status": "paid",
        "timestamp": "2025-12-07T11:30:00Z"
      },
      {
        "status": "packed",
        "timestamp": "2025-12-07T12:00:00Z"
      },
      {
        "status": "shipped",
        "timestamp": "2025-12-07T13:00:00Z"
      },
      {
        "status": "out_for_delivery",
        "timestamp": "2025-12-07T14:00:00Z"
      }
    ]
  }
}
```

---

## 4.4 Cancel Order

```http
POST /api/v1/orders/{orderId}/cancel
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "orderId": "order-uuid",
    "status": "cancelled",
    "message": "Order cancelled successfully"
  }
}
```

**Notes:**
- Hanya bisa cancel order dengan status `pending_payment`
- Order yang sudah `packed`, `shipped`, `out_for_delivery`, atau `delivered` tidak bisa dicancel

---

## 4.5 Get Shipment Detail

```http
GET /api/v1/orders/{orderId}/shipment
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": {
    "orderId": "order-uuid",
    "trackingNumber": "JP1234567890",
    "courier": "JNE",
    "service": "REG",
    "status": "on_delivery",
    "statusLabel": "Dalam Pengiriman",
    "estimatedDelivery": "2-3 hari",
    "shippedAt": "2025-12-07T14:00:00Z",
    "tracking": [
      {
        "timestamp": "2025-12-07T14:00:00Z",
        "status": "picked_up",
        "location": "Jakarta Warehouse",
        "description": "Paket telah diambil kurir"
      },
      {
        "timestamp": "2025-12-07T16:30:00Z",
        "status": "in_transit",
        "location": "Jakarta Distribution Center",
        "description": "Paket dalam perjalanan"
      },
      {
        "timestamp": "2025-12-08T08:00:00Z",
        "status": "on_delivery",
        "location": "Jakarta Selatan",
        "description": "Paket sedang diantar ke alamat tujuan"
      }
    ]
  }
}
```

---

## 4.6 Download Invoice (PDF)

```http
GET /api/v1/orders/{orderId}/invoice
Authorization: Bearer <token>
```

Returns the invoice for one of the caller's own orders in the active tenant, as
a PDF. Replaces the client-side `.txt` invoice.

**Response:** `200 OK`

| Header                | Value |
|-----------------------|-------|
| `Content-Type`        | `application/pdf` |
| `Content-Disposition` | `attachment; filename="INV-2026-000001.pdf"` |
| `Cache-Control`       | `private, no-store` |
| `Content-Length`      | PDF size in bytes |

Body: the PDF bytes. Fetch it as a blob, for example
`fetch(url, { headers }).then(r => r.blob())`, and take the download name from
`Content-Disposition`.

**Eligibility:** a new invoice is issued only for orders with status `paid`,
`packed`, `shipped`, `out_for_delivery` or `delivered`. Orders in
`pending_payment` or `cancelled` get `409 INVOICE_NOT_AVAILABLE`. Once issued,
an invoice stays downloadable even if the order is later cancelled or refunded.
The payment status line then shows the current state.

**Invoice number:** `INV-YYYY-NNNNNN`. `YYYY` is the issue year in the tenant's
timezone. `NNNNNN` is a per-tenant counter, zero-padded and never reset. The
number is assigned on the first download (by the customer or an admin) and
never changes after that. Repeated downloads return the same number.

**PDF content:** store name, email, phone and description (from admin settings,
falling back to the tenant name and contact email), invoice number and issue
date, order number and order date, payment status and channel, order status,
customer name and email, shipping address, line items (title, SKU, qty, unit
price, subtotal), and subtotal, discount (with voucher code), tax, shipping and
total. Amounts are whole rupiah formatted as `Rp 1.250.000`, the same values as
the order detail response. Labels are in Indonesian. Text is encoded as
Windows-1252, so Latin-1 letters and typographic punctuation (`é`, `’`, `–`)
render as-is and any other character renders as `?`. The SKU column shows the
variant's current SKU (order lines do not snapshot SKUs); titles, quantities
and amounts are the order-time snapshot.

An order cancelled at the same moment as its first invoice request is not
issued a number: eligibility is re-checked under a row lock inside the
numbering transaction.

**Errors** (JSON envelope):

| Status | `error.code`            | When |
|--------|-------------------------|------|
| 400    | `BAD_REQUEST`           | `orderId` is not a UUID. |
| 401    | `UNAUTHORIZED`          | Missing/invalid token. |
| 404    | `NOT_FOUND`             | Order does not exist, belongs to another user, or belongs to another tenant. |
| 409    | `INVOICE_NOT_AVAILABLE` | Order not paid yet (or cancelled) and no invoice was issued before. |
| 500    | `INTERNAL`              | Rendering or database failure. |

Admin equivalent: `GET /api/v1/admin/orders/{id}/invoice` ([Admin 6.20](admin.md#620-download-order-invoice-pdf)).
