# Admin Endpoints

> **Last Updated:** 2026-08-07

All admin endpoints require **admin authentication**.

## 6.1 List Flash Sale Campaigns

```http
GET /api/v1/admin/flash-sales?page=1&limit=20
Authorization: Bearer <admin_token>
```

Returns a paginated list of flash sale campaigns with their items.

**Query parameters:**

- `page` — page number (default: 1)
- `limit` — page size, capped at `100` (default: 20)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "campaign-uuid",
      "name": "Summer Flash Sale",
      "slug": "summer-flash-sale",
      "status": "ACTIVE",
      "startsAt": "2025-12-01T00:00:00Z",
      "endsAt": "2025-12-05T23:59:59Z",
      "createdAt": "2025-12-01T00:00:00Z",
      "updatedAt": "2025-12-01T00:00:00Z",
      "items": [
        {
          "id": "item-uuid",
          "productId": "product-uuid",
          "title": "MacBook Pro 14 M3",
          "slug": "macbook-pro-14-m3",
          "originalPrice": 25000000,
          "salePrice": 20000000,
          "discountBps": 2000,
          "stock": 10,
          "stockLimit": 10,
          "soldCount": 0,
          "thumbnail": "https://cdn.toko.com/products/mbp14.jpg"
        }
      ]
    }
  ],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 3
  }
}
```

**Campaign status values:**

- `DRAFT` — not yet published
- `SCHEDULED` — scheduled for future start
- `ACTIVE` — currently running
- `ENDED` — campaign has ended

**Item fields:**

- `stock` — remaining available stock (stockLimit - soldCount, or total variant stock if no limit)
- `stockLimit` — optional per-campaign stock cap (nullable)
- `soldCount` — number of units sold in this campaign
- `discountBps` — discount in basis points (e.g., 2000 = 20%)

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `INTERNAL`.

---

## 6.2 Get Flash Sale Campaign

```http
GET /api/v1/admin/flash-sales/{id}
Authorization: Bearer <admin_token>
```

Returns a single flash sale campaign with all its items.

**Path parameter:**

- `id` — campaign UUID

**Response:** `200 OK`

```json
{
  "data": {
    "id": "campaign-uuid",
    "name": "Summer Flash Sale",
    "slug": "summer-flash-sale",
    "status": "ACTIVE",
    "startsAt": "2025-12-01T00:00:00Z",
    "endsAt": "2025-12-05T23:59:59Z",
    "createdAt": "2025-12-01T00:00:00Z",
    "updatedAt": "2025-12-01T00:00:00Z",
    "items": [
      {
        "id": "item-uuid",
        "productId": "product-uuid",
        "title": "MacBook Pro 14 M3",
        "slug": "macbook-pro-14-m3",
        "originalPrice": 25000000,
        "salePrice": 20000000,
        "discountBps": 2000,
        "stock": 10,
        "stockLimit": 10,
        "soldCount": 0,
        "thumbnail": "https://cdn.toko.com/products/mbp14.jpg"
      }
    ]
  }
}
```

**Response:** `404 Not Found` if campaign doesn't exist.

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `NOT_FOUND`, `INTERNAL`.

---

## 6.3 Create Flash Sale Campaign

```http
POST /api/v1/admin/flash-sales
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Creates a new flash sale campaign with product items.

**Request:**

```json
{
  "name": "Summer Flash Sale",
  "slug": "summer-flash-sale",
  "startsAt": "2025-12-01T00:00:00Z",
  "endsAt": "2025-12-05T23:59:59Z",
  "items": [
    {
      "productId": "product-uuid",
      "salePrice": 20000000,
      "stockLimit": 10
    }
  ]
}
```

- `name` — required, campaign name
- `slug` — required, unique URL-friendly identifier
- `startsAt` / `endsAt` — required, ISO 8601 timestamps
- `items` — required array of product items
  - `productId` — required, product UUID
  - `salePrice` — required, sale price in IDR
  - `stockLimit` — optional, per-campaign stock cap (uses product variant stock if omitted)

**Response:** `201 Created`

```json
{
  "data": {
    "id": "campaign-uuid",
    "name": "Summer Flash Sale",
    "slug": "summer-flash-sale",
    "status": "DRAFT",
    "startsAt": "2025-12-01T00:00:00Z",
    "endsAt": "2025-12-05T23:59:59Z",
    "createdAt": "2025-12-01T00:00:00Z",
    "updatedAt": "2025-12-01T00:00:00Z",
    "items": [...]
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `CONFLICT` (duplicate slug), `INTERNAL`.

---

## 6.4 Update Flash Sale Campaign

```http
PATCH /api/v1/admin/flash-sales/{id}
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Updates a flash sale campaign. Can update status, dates, and items.

**Request:**

```json
{
  "name": "Updated Summer Sale",
  "status": "ACTIVE",
  "startsAt": "2025-12-01T00:00:00Z",
  "endsAt": "2025-12-10T23:59:59Z",
  "items": [
    {
      "id": "existing-item-uuid",
      "productId": "product-uuid",
      "salePrice": 19000000,
      "stockLimit": 15
    }
  ]
}
```

- `status` — optional, one of `DRAFT`, `SCHEDULED`, `ACTIVE`, `ENDED`
- `items` — optional, full replacement of items. Include `id` for existing items to update; omit `id` for new items.

**Response:** `200 OK`

```json
{
  "data": { ...campaign object... }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `CONFLICT`, `INTERNAL`.

---

## 6.5 Create Voucher

```http
POST /api/v1/admin/vouchers
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Creates a new voucher rule.

**Request:**

```json
{
  "code": "DISC20",
  "kind": "percent",
  "value": 0,
  "percentBps": 2000,
  "minSpend": 100000,
  "usageLimit": 100,
  "perUserLimit": 1,
  "validFrom": "2025-12-01T00:00:00Z",
  "validTo": "2025-12-31T23:59:59Z",
  "productIds": ["product-uuid"],
  "categoryIds": ["category-uuid"],
  "brandIds": ["brand-uuid"],
  "combinable": false,
  "priority": 10
}
```

- `code` — required, unique voucher code
- `kind` — required, either `fixed_amount` or `percent`
- `value` — discount value in IDR (used when `kind` is `fixed_amount`)
- `percentBps` — basis points (e.g. `2000` = 20%) when `kind` is `percent`
- `minSpend` — minimum cart total to apply the voucher
- `usageLimit` — global usage cap (optional)
- `perUserLimit` — usage cap per user (optional)
- `validFrom` / `validTo` — ISO 8601 timestamps (optional)
- `productIds`, `categoryIds`, `brandIds` — restriction lists (optional)
- `combinable` — whether the voucher can be combined with others (default `false`)
- `priority` — evaluation priority (optional, server default applies if omitted)

**Response:** `201 Created`

```json
{
  "data": {
    "id": "voucher-uuid",
    "code": "DISC20",
    "kind": "percent",
    "value": 0,
    "percent_bps": 2000,
    "min_spend": 100000,
    "usage_limit": 100,
    "used_count": 0,
    "per_user_limit": 1,
    "valid_from": "2025-12-01T00:00:00Z",
    "valid_to": "2025-12-31T23:59:59Z",
    "product_ids": ["product-uuid"],
    "category_ids": ["category-uuid"],
    "brand_ids": ["brand-uuid"],
    "combinable": false,
    "priority": 10,
    "created_at": "2025-12-01T00:00:00Z",
    "updated_at": "2025-12-01T00:00:00Z",
    "tenant_id": "tenant-uuid"
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `CONFLICT` (duplicate code), `INTERNAL`.

---

## 6.6 Update Voucher

```http
PUT /api/v1/admin/vouchers/{code}
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Updates the voucher identified by its `code`. The request body uses the same shape as creation.

**Response:** `200 OK`

```json
{
  "data": { ...voucher object... }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `INTERNAL`.

---

## 6.7 Preview Voucher

```http
POST /api/v1/admin/vouchers/preview
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Dry-run evaluation of a voucher for a given cart context without persisting state.

**Request:**

```json
{
  "code": "DISC20",
  "cartTotal": 250000,
  "userId": "user-uuid",
  "items": [
    {
      "productId": "product-uuid",
      "categoryId": "category-uuid",
      "brandId": "brand-uuid",
      "subtotal": 250000
    }
  ]
}
```

- `code` — required
- `cartTotal` — required, cart total in IDR
- `userId` — optional, used for per-user limit checks
- `items` — required, at least one valid item with `subtotal > 0`

**Response:** `200 OK`

```json
{
  "data": {
    "discount": 50000,
    "eligible_amount": 250000,
    "code": "DISC20"
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_ELIGIBLE` (voucher cannot be applied), `INTERNAL`.

---

## 6.8 Update Order Status

```http
PATCH /api/v1/admin/orders/{id}/status
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Advances the order status using state-machine validation. The `{id}` path parameter is the order UUID.

**Request:**

```json
{
  "status": "PACKED"
}
```

**Valid target statuses:**

- `PACKED`
- `SHIPPED`
- `OUT_FOR_DELIVERY`
- `DELIVERED`
- `CANCELLED`

**Typical transitions:**

- `PENDING_PAYMENT` → `PAID`, `CANCELLED`
- `PAID` → `PACKED`, `CANCELLED`
- `PACKED` → `SHIPPED`
- `SHIPPED` → `OUT_FOR_DELIVERY`
- `OUT_FOR_DELIVERY` → `DELIVERED`

The backend rejects transitions to an equal or previous state in the status rank.

**Response:** `204 No Content`

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `INVALID_STATE` (transition not allowed), `INTERNAL`.

---

## 6.9 Create Shipment

```http
POST /api/v1/admin/orders/{id}/shipment
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Registers courier and tracking data for an order. The `{id}` path parameter is the order UUID.

**Request:**

```json
{
  "courier": "jne",
  "trackingNumber": "JP1234567890"
}
```

**Response:** `201 Created`

```json
{
  "data": {
    "id": "shipment-uuid",
    "orderId": "order-uuid",
    "status": "pending",
    "courier": "jne",
    "trackingNumber": "JP1234567890",
    "lastStatus": null,
    "lastEventAt": null,
    "events": []
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `INVALID_STATE` (order not eligible), `ALREADY_EXISTS`, `INTERNAL`.

---

## 6.10 Create Webhook Endpoint

```http
POST /api/v1/admin/webhooks
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Registers a new webhook endpoint.

**Request:**

```json
{
  "name": "Inventory sync",
  "url": "https://partner.example.com/webhooks",
  "secret": "webhook-secret",
  "active": true,
  "topics": ["order.paid", "order.shipped"]
}
```

- `name`, `url`, `secret` — required
- `active` — optional, defaults to `true`
- `topics` — optional array of topic strings; empty means all topics

**Response:** `201 Created`

Note: the backend returns the endpoint object directly, not wrapped in `{ "data": ... }`.

```json
{
  "id": "endpoint-uuid",
  "name": "Inventory sync",
  "url": "https://partner.example.com/webhooks",
  "secret": "webhook-secret",
  "active": true,
  "topics": ["order.paid", "order.shipped"],
  "created_at": "2025-12-01T00:00:00Z",
  "updated_at": "2025-12-01T00:00:00Z",
  "tenant_id": "tenant-uuid"
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `INTERNAL`.

---

## 6.11 Update Webhook Endpoint

```http
PUT /api/v1/admin/webhooks/{id}
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Updates the webhook endpoint identified by `{id}`.

**Request:** same shape as creation.

**Response:** `200 OK`

Note: the backend returns the endpoint object directly, not wrapped in `{ "data": ... }`.

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `INTERNAL`.

---

## 6.12 List Webhook Endpoints

```http
GET /api/v1/admin/webhooks?limit=50&offset=0
Authorization: Bearer <admin_token>
```

Returns configured webhook endpoints.

**Query parameters:**

- `limit` — page size, capped at `200` (default `50`)
- `offset` — items to skip (default `0`)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "endpoint-uuid",
      "name": "Inventory sync",
      "url": "https://partner.example.com/webhooks",
      "secret": "webhook-secret",
      "active": true,
      "topics": ["order.paid"],
      "created_at": "2025-12-01T00:00:00Z",
      "updated_at": "2025-12-01T00:00:00Z",
      "tenant_id": "tenant-uuid"
    }
  ]
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `INTERNAL`.

---

## 6.13 Delete Webhook Endpoint

```http
DELETE /api/v1/admin/webhooks/{id}
Authorization: Bearer <admin_token>
```

Removes the webhook endpoint identified by `{id}`.

**Response:** `204 No Content`

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `INTERNAL`.

---

## 6.14 List Webhook Deliveries

```http
GET /api/v1/admin/webhook-deliveries?endpointId=&eventId=&status=&limit=50&offset=0
Authorization: Bearer <admin_token>
```

Returns webhook delivery attempts with optional filtering.

**Query parameters:**

- `endpointId` — optional UUID filter
- `eventId` — optional UUID filter
- `status` — optional status filter
- `limit` — page size, capped at `200` (default `50`)
- `offset` — items to skip (default `0`)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "delivery-uuid",
      "endpoint_id": "endpoint-uuid",
      "event_id": "event-uuid",
      "status": "PENDING",
      "attempt": 1,
      "max_attempt": 3,
      "next_attempt_at": "2025-12-01T00:05:00Z",
      "last_error": null,
      "response_status": null,
      "response_body": null,
      "created_at": "2025-12-01T00:00:00Z",
      "updated_at": "2025-12-01T00:00:00Z",
      "tenant_id": "tenant-uuid"
    }
  ],
  "total": 42
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `INTERNAL`.

---

## 6.15 Replay Webhook Delivery

```http
POST /api/v1/admin/webhook-deliveries/{id}/replay
Authorization: Bearer <admin_token>
```

Resets the delivery identified by `{id}` for retry and releases any related DLQ lock.

**Response:** `200 OK`

Note: the backend returns the delivery object directly, not wrapped in `{ "data": ... }`.

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `NOT_FOUND`, `INTERNAL`.

---

## 6.16 List Dead-Letter Queue (DLQ) Items

```http
GET /api/v1/admin/queue/dlq?kind=webhook&limit=50&offset=0
Authorization: Bearer <admin_token>
```

Returns DLQ entries filtered by kind with pagination.

**Query parameters:**

- `kind` — optional queue kind filter (e.g. `webhook`)
- `limit` — page size, capped at `200` (handler default `50`)
- `offset` — items to skip (default `0`)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "id": "dlq-uuid",
      "kind": "webhook",
      "idempotencyKey": "idem-key",
      "attempts": 3,
      "lastError": "connection timeout",
      "createdAt": "2025-12-01T00:00:00Z",
      "message": {
        "kind": "webhook",
        "key": "idem-key",
        "payload": {},
        "attempt": 3,
        "max_attempts": 5,
        "available_at": 1733600000000
      }
    }
  ],
  "total": 12,
  "kind": "webhook"
}
```

Note: `kind` is only included in the response when a kind filter was applied.

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `INTERNAL`.

---

## 6.17 Replay DLQ Items

```http
POST /api/v1/admin/queue/dlq/replay
Content-Type: application/json
Authorization: Bearer <admin_token>
```

Re-enqueues DLQ entries either by ID list or by batch kind.

**Request:**

```json
{
  "ids": ["dlq-uuid-1", "dlq-uuid-2"],
  "kind": "webhook",
  "limit": 20
}
```

- `ids` — optional array of DLQ entry UUIDs to replay individually
- `kind` — optional queue kind to replay in batch
- `limit` — cap for batch replay when using `kind` (default handler page size)

At least one of `ids` or `kind` is required. When `ids` is provided, `kind` is ignored for those IDs.

**Response:** `200 OK`

```json
{
  "replayed": ["dlq-uuid-1"],
  "failed": {
    "dlq-uuid-2": "invalid payload"
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `INTERNAL`.

---

## 6.18 Get Queue Stats

```http
GET /api/v1/admin/queue/stats?kind=webhook
Authorization: Bearer <admin_token>
```

Returns queue depth, in-flight count, DLQ size, and oldest lag for a given queue kind.

**Query parameters:**

- `kind` — required queue kind (e.g. `webhook`)

**Response:** `200 OK`

```json
{
  "kind": "webhook",
  "ready": 15,
  "processing": 2,
  "dlq": 3,
  "oldest_lag_ms": 45000,
  "visibility_timeout": 60
}
```

- `ready` — tasks waiting in the queue
- `processing` — tasks currently being processed
- `dlq` — dead-lettered tasks
- `oldest_lag_ms` — lag of the oldest ready task in milliseconds
- `visibility_timeout` — configured visibility timeout in seconds

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `INTERNAL`.

---

## 6.19 List Audit Logs

```http
GET /api/v1/admin/audit-logs?limit=50&offset=0
Authorization: Bearer <admin_token>
```

Returns a paginated list of audit logs.

**Query parameters:**

- `limit` — page size, capped at `200` (default `50`)
- `offset` — items to skip (default `0`)

**Response:** `200 OK`

Note: the backend currently returns a raw array, not the standard `{ "data": ... }` envelope.

```json
[
  {
    "id": "audit-uuid",
    "actor_kind": "user",
    "actor_user_id": "user-uuid",
    "action": "UPDATE",
    "resource_type": "order",
    "resource_id": "order-uuid",
    "method": "PATCH",
    "path": "/api/v1/admin/orders/order-uuid/status",
    "route": "/api/v1/admin/orders/{id}/status",
    "status": 204,
    "ip": "127.0.0.1",
    "user_agent": "Mozilla/5.0",
    "request_id": "req-uuid",
    "metadata": {},
    "created_at": "2025-12-01T00:00:00Z"
  }
]
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `AUDIT_NOT_CONFIGURED`, `AUDIT_QUERY_FAILED`, `INTERNAL`.