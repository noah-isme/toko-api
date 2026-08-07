# In-App Notifications

> **Last Updated:** 2026-08-07

Per-user in-app notifications. Notifications are created automatically as a side
effect of order and shipment lifecycle events (see [Webhooks](webhooks.md) for
the underlying domain topics) and read back through the endpoints below.

All endpoints require a Bearer token and are tenant-scoped: a user only ever
sees notifications belonging to their own account within the active tenant.

Base path: `/api/v1/notifications`

## Notification object

```json
{
  "id": "b3f1c8a0-1111-2222-3333-444455556666",
  "type": "order_paid",
  "title": "Pembayaran diterima",
  "body": "Pembayaran pesanan Anda telah kami terima.",
  "data": { "orderId": "order-uuid", "topic": "order.paid" },
  "read": false,
  "readAt": null,
  "createdAt": "2026-07-22T10:00:00Z"
}
```

| Field       | Type             | Description                                              |
|-------------|------------------|----------------------------------------------------------|
| `id`        | string (uuid)    | Notification identifier.                                  |
| `type`      | string           | Machine-readable kind (see table below).                 |
| `title`     | string           | Short headline, safe to display as-is.                   |
| `body`      | string           | Longer message body.                                     |
| `data`      | object           | Arbitrary JSON context; always present (`{}` if empty).  |
| `read`      | boolean          | `true` once the notification has been marked read.       |
| `readAt`    | string \| null   | RFC 3339 timestamp the notification was read, or `null`. |
| `createdAt` | string           | RFC 3339 creation timestamp.                              |

### Notification types

| `type`                     | Emitted when                          |
|----------------------------|---------------------------------------|
| `order_paid`               | Payment for an order is confirmed.    |
| `order_canceled`           | An order is canceled.                 |
| `payment_failed`           | A payment attempt fails.              |
| `payment_expired`          | A payment window expires.             |
| `shipment_shipped`         | A shipment is handed to the courier.  |
| `shipment_out_for_delivery`| A shipment is out for delivery.       |
| `shipment_delivered`       | A shipment is delivered.              |

---

## List notifications

```http
GET /api/v1/notifications?page=1&limit=20
Authorization: Bearer <token>
```

Returns the user's notifications, most recent first.

**Query parameters**

| Name    | Default | Notes                          |
|---------|---------|--------------------------------|
| `page`  | `1`     | 1-based page number.           |
| `limit` | `20`    | Page size, clamped to `100`.   |

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "b3f1c8a0-1111-2222-3333-444455556666",
      "type": "shipment_delivered",
      "title": "Pesanan tiba",
      "body": "Pesanan Anda telah sampai di tujuan.",
      "data": { "orderId": "order-uuid", "topic": "shipment.delivered" },
      "read": false,
      "readAt": null,
      "createdAt": "2026-07-22T10:00:00Z"
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 1
  }
}
```

---

## Unread count

```http
GET /api/v1/notifications/unread-count
Authorization: Bearer <token>
```

Lightweight endpoint for a badge counter.

**Response:** `200 OK`
```json
{ "unread": 3 }
```

---

## Mark one as read

```http
POST /api/v1/notifications/{id}/read
Authorization: Bearer <token>
```

Idempotent: marking an already-read, non-existent, or another user's
notification returns `200 OK` without error (nothing is leaked and repeated
calls are safe).

**Response:** `200 OK`
```json
{ "read": true }
```

---

## Mark all as read

```http
POST /api/v1/notifications/read-all
Authorization: Bearer <token>
```

Marks every unread notification for the user as read.

**Response:** `204 No Content`

---

## Errors

| Status | `code`             | When                                   |
|--------|--------------------|----------------------------------------|
| `401`  | `UNAUTHORIZED`     | Missing or invalid access token.       |
| `400`  | `MISSING_TENANT`   | Tenant context could not be resolved.  |
| `400`  | `INVALID_ID`       | Notification id is not a valid UUID.   |
| `500`  | `LIST_FAILED` / `COUNT_FAILED` / `MARK_FAILED` | Unexpected persistence error. |

Errors use the canonical envelope:
```json
{ "error": { "code": "UNAUTHORIZED", "message": "UNAUTHORIZED" } }
```
