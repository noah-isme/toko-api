# Admin Endpoints

> **Last Updated:** 2026-10-25

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
  - `stockLimit` — optional, per-campaign stock cap, integer >= 1; `null` or absent means unlimited (product variant stock applies). `0` is rejected with `400`.

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

Updates a flash sale campaign. All fields are optional (absent = unchanged, at least one is required), so a bare `{ "status": "ENDED" }` body still works. The update is applied in a single transaction, scoped to the caller's tenant.

**Request:**

```json
{
  "name": "Updated Summer Sale",
  "slug": "updated-summer-sale",
  "status": "ACTIVE",
  "startsAt": "2025-12-01T00:00:00Z",
  "endsAt": "2025-12-10T23:59:59+07:00",
  "items": [
    {
      "id": "existing-item-uuid",
      "productId": "product-uuid",
      "salePrice": 19000000,
      "stockLimit": 15
    },
    { "productId": "new-product-uuid", "salePrice": 9000000, "stockLimit": null }
  ]
}
```

Rules:

- `name`, `slug` — optional, non-empty when present.
- `status` — optional, one of `DRAFT`, `SCHEDULED`, `ACTIVE`, `ENDED` (any transition is allowed; an empty string is rejected). `SCHEDULED` and `ACTIVE` require the resulting `endsAt` to be in the future. `ENDED` campaigns can still be edited and rescheduled.
- `startsAt`, `endsAt` — optional, **RFC 3339 with a timezone** (e.g. `2025-12-01T00:00:00Z`, `2025-12-10T23:59:59+07:00`). Values without a timezone (e.g. `2025-12-01T00:00`) are rejected with `400`. The resulting `endsAt` must be after `startsAt`.
- `items` — optional; when present it is a **full replacement** keyed by item `id`:
  - an entry with `id` updates that existing item (the `id` must belong to this campaign);
  - an entry without `id` adds a new item;
  - existing items missing from the list are removed.
  - At least one item is required (`items: []` is rejected). `productId` values must be unique within the request and must belong to the caller's tenant.
- `salePrice` — integer >= 0 and **lower than the product's current price**.
- `stockLimit` — `null` = unlimited; for an entry with `id`, an absent key keeps the item's current limit; for a new entry absent = unlimited. An integer must be >= 1 and >= the item's `soldCount`; `0` is rejected with `400`.
- Items with sales (`soldCount > 0`) cannot be removed (`409`) or re-pointed to another product (`400`). Their `salePrice` can change, which affects future carts only.
- Products cannot be swapped between two existing items in one request (`400`); remove one item and add it again. Moving an item to a product freed by another item in the same request is allowed.
- Removing an item without sales also discards its released reservation history (`RELEASED` rows of abandoned checkouts). Items with active (`RESERVED`) or committed reservations cannot be removed.
- Editing items of an active campaign affects carts that hold a removed or changed product: checkout reports that the flash sale is no longer active for that line.

**Response:** `200 OK` with the full campaign (same shape as `GET /api/v1/admin/flash-sales/{id}`); `items` is `[]` when the campaign has none.

```json
{
  "data": {
    "id": "campaign-uuid",
    "name": "Updated Summer Sale",
    "slug": "updated-summer-sale",
    "status": "ACTIVE",
    "startsAt": "2025-12-01T00:00:00Z",
    "endsAt": "2025-12-10T16:59:59Z",
    "createdAt": "2025-11-01T08:00:00Z",
    "updatedAt": "2025-11-20T10:00:00Z",
    "items": [
      {
        "id": "item-uuid",
        "productId": "product-uuid",
        "title": "Product title",
        "slug": "product-slug",
        "originalPrice": 25000000,
        "salePrice": 19000000,
        "discountBps": 2400,
        "stock": 15,
        "stockLimit": 15,
        "soldCount": 0,
        "thumbnail": "https://..."
      }
    ]
  }
}
```

**Errors:**

- `400 BAD_REQUEST` — invalid payload, non-RFC 3339 timestamps, no updatable fields, invalid status, bad date range, empty `items`, duplicate `productId`/`id`, unknown item `id`, product not found in this tenant, `salePrice` not lower than the product price, `stockLimit` below 1 or `soldCount`, product change on an item with sales, product swap.
- `404 NOT_FOUND` — campaign does not exist for this tenant.
- `409 CONFLICT` — slug already used in this tenant; product already in this campaign; removing an item that has sales or active reservations (`details.itemId`); campaign busy (lock timeout or deadlock), retry.
- Also `UNAUTHORIZED`, `FORBIDDEN`, `INTERNAL`.

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
---

# Catalog operations (6.20–6.28)

All routes below are under `/api/v1/admin`, need a bearer admin token and `X-Tenant-ID`, and use the standard envelope `{"data": ...}` / `{"error": {"code", "message", "details"}}`. Money is in minor units (integers); timestamps are RFC3339 UTC. Full schemas: `openapi.yaml` (each route carries `x-required-permission`).

### Required permissions (for the RBAC integration)

This branch still guards the whole `/admin` group with the `admin` role. When the RBAC stream lands, register each route with these permissions (fail closed):

| Route | Permission(s) |
|---|---|
| `POST /admin/products/bulk/price`, `POST /admin/products/bulk/status` | `catalog:write` |
| `POST /admin/products/bulk/stock` | `inventory:write` |
| `GET /admin/products/export` | `catalog:read` |
| `POST /admin/products/import` | `catalog:write` **and** `inventory:write` |
| `GET /admin/orders/export` | `orders:read` **and** `customers:read` (the file has customer name/email) |
| `GET /admin/inventory` (new filters), `GET /admin/analytics/overview` (new counts) | unchanged (`inventory:read`, `analytics:read`) |

The frontend should hide bulk/import/export controls when `/auth/me.permissions` lacks them.

## 6.20 Product List: Sort, Status and Stock Filters

```http
GET /api/v1/admin/products?page=1&limit=20&sort=price:asc&status=published&stockStatus=low
```

Backward compatible: existing parameters keep their meaning; omitting `sort` keeps the previous order.

- `sort` — `name|price|stock|created|updated`, optional `:asc|:desc`. Bare `name|price|stock` sort ascending, bare `created|updated` descending. Default `created:desc`. `stock` = sum of variant stock. Invalid → `400 BAD_REQUEST`.
- `status` — `draft|published|archived` (omit for all). Invalid → 400.
- `stockStatus` — `in|low|out` (see 6.26). Invalid → 400.

Each `AdminProduct` (list and detail) gains:

```json
{ "status": "published", "stockStatus": "low", "lowStockThreshold": null }
```

`lowStockThreshold` is the product override (`null` = tenant default). `POST /admin/products` and `PATCH /admin/products/{id}` accept `status` (default `published`) and `lowStockThreshold` (0..1000000; `null` on PATCH clears the override). A draft or archived product disappears from the storefront (list, detail → 404, search, related, recommendations, favourites, flash sales) and cannot be added to a cart (`400 BAD_REQUEST`, "product is not available"); a line added earlier fails at checkout with `409 PRODUCT_UNAVAILABLE`, `details: {productId, slug}`, and nothing is reserved or ordered.

## 6.21 Bulk Price Update

```http
POST /api/v1/admin/products/bulk/price
Content-Type: application/json

{
  "items": [
    { "productId": "p-uuid-1", "price": 125000, "compareAt": 150000 },
    { "productId": "p-uuid-2", "price": 99000, "applyTo": "product" },
    { "variantId": "v-uuid-9", "price": 105000 }
  ]
}
```

- Product item: `productId`, `price`, optional `compareAt` (omit = keep, `null` = clear), optional `applyTo`:
  - `both` (default): sets the product price **and every variant price**, so listing and cart agree (the cart charges the variant price). Flattens per-variant price differences.
  - `product`: listing price only; the cart keeps charging each variant's own price.
  - `variants`: variant prices only (`compareAt` not allowed).
- Variant item: `variantId`, `price` (no `applyTo`/`compareAt`). A variant whose product is also targeted with `applyTo` `both|variants` is a `DUPLICATE`.

**Response:** `200 OK`

```json
{
  "data": {
    "updated": 3,
    "items": [
      { "productId": "p-uuid-1", "slug": "sepatu-lari", "price": 125000, "compareAt": 150000, "applyTo": "both", "variantsUpdated": 2 },
      { "productId": "p-uuid-2", "slug": "kaos", "price": 99000, "compareAt": null, "applyTo": "product", "variantsUpdated": 0 },
      { "variantId": "v-uuid-9", "productId": "p-uuid-3", "sku": "TOPI-1", "price": 105000 }
    ]
  }
}
```

## 6.22 Bulk Stock Update

```http
POST /api/v1/admin/products/bulk/stock

{ "items": [ { "variantId": "v-uuid-1", "stock": 40 }, { "variantId": "v-uuid-2", "delta": -3 } ] }
```

Each item sends exactly one of `stock` (absolute, ≥ 0) or `delta`. Deltas apply against the **live** stock after the row is locked, so concurrent checkouts are respected (stock 10, a checkout reserves 3, `delta +5` → 12). A result below zero rejects the row (`NEGATIVE_RESULT`); nothing is clamped. A result above 2147483647 is `OUT_OF_RANGE`. Products are locked before their variants (the same order as bulk price and import), so concurrent catalog writers do not deadlock; see the lock rules below for how this interacts with checkout. `inStock` of each touched product is recomputed.

**Response:** `200 OK` — `{"data": {"updated": 2, "items": [{"variantId", "productId", "sku", "stock", "inStock"}]}}` (`stock` after the update; `inStock` is `null` for products that did not change).

## 6.23 Bulk Status (Publish / Draft / Archive)

```http
POST /api/v1/admin/products/bulk/status

{ "items": [ { "productId": "p-uuid-1", "status": "draft" } ] }
```

**Response:** `200 OK` — `{"data": {"updated": 1, "items": [{"productId", "slug", "status"}]}}`. Visibility effects as in 6.20.

### Bulk rules (6.21–6.23)

- All-or-nothing; 1..500 items; body ≤ 1 MiB; unknown JSON fields are rejected.
- Targets are locked in id order with a 5 s lock timeout, so a bulk request never stalls checkout for longer.
- Lock order and strength (bulk price, bulk stock and import alike): products first, then variants, each in id order, all with `FOR NO KEY UPDATE`. Catalog writers therefore serialize against each other, but the product and variant locks do not conflict with the `FOR KEY SHARE` locks that foreign-key checks take. Checkout reserves stock (row-locks the variant) and then inserts `inventory_reservations` (key-share on the product), and add-to-cart inserts `cart_items` (key-share on product and variant). Add-to-cart never waits on a running catalog write; checkout waits only when it reserves a variant the catalog write already holds (and vice versa, bounded by the 5 s timeout), and checkout's variant-then-product order cannot form a cycle with the catalog writer's product-then-variant order.
- Residual cases, bounded by the 5 s lock timeout and answered as `409 CONFLICT` (retry): an import that changes a product's `slug` or a variant's `sku` (unique columns) upgrades that row lock to `FOR UPDATE` strength when it writes, which conflicts with key-share again for that row; a multi-item checkout that locks two variants in the opposite order to the catalog writer's id order can deadlock with it (as two such checkouts already can with each other). Postgres aborts one side; a checkout that loses gets `409 CONFLICT` with `details.retryable: true` (see the checkout contract) instead of the raw driver error.
- Errors (nothing is changed in any error case):
  - `400 BAD_REQUEST` (malformed, empty `items`) or `400 TOO_MANY_ROWS` (`details: {max, got}`)
  - `413 PAYLOAD_TOO_LARGE` (JSON envelope; the global `SECURITY_BODY_LIMIT_BYTES` cap, 1 MiB by default, answers in the same shape)
  - `409 CONFLICT` — a concurrent change held a lock > 5 s or a deadlock was detected; retry
  - `422 VALIDATION_FAILED` with every bad item:

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "one or more items are invalid; nothing was changed",
    "details": { "rows": [ { "index": 1, "field": "price", "code": "NEGATIVE", "message": "price must be zero or greater" } ] }
  }
}
```

  Row codes: `MISSING`, `AMBIGUOUS` (both ids, or both `stock` and `delta`), `INVALID_UUID`, `NEGATIVE`, `NOT_FOUND` (missing or another store's id; never distinguished), `DUPLICATE`, `INVALID_STATUS`, `INVALID_APPLY_TO`, `NEGATIVE_RESULT`, `OUT_OF_RANGE`, `NOT_ALLOWED`.
- Side effects match single updates: catalog cache invalidation and one `product.*` event per changed value (6.28).
- Audit: besides the `/admin` middleware row, each bulk request that changed something writes one explicit row (`catalog.bulk.price|stock|status`, resource `catalog_bulk`) **in the same transaction**, with `metadata.items[] = {productId, variantId?, sku?, slug, changes: {field: {from, to}}}`, the actor user id and the server request id (chi's request id; a client-sent `X-Request-ID` is reused, which is how the row can be correlated with the client's request). The same request id and actor appear in the `product.*` event payloads.

## 6.24 Export Products (CSV)

```http
GET /api/v1/admin/products/export?search=&category=&brand=&inStock=&status=&stockStatus=
```

Streams `text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="products-YYYYMMDD-HHMMSS.csv"`. One row per variant; a product without variants is one row with empty variant columns. Same filters as 6.20, no pagination.

Columns, in order: `sku, product_id, variant_id, title, slug, status, price, compare_at, variant_price, stock, stock_baseline, low_stock_threshold, in_stock, stock_status, category_slug, brand_slug, description, attributes, created_at, updated_at`.

- `stock_baseline` repeats `stock` at export time. Leave it alone when editing; the import uses it to protect sales made since the export (6.25).
- `attributes` is a JSON object; `in_stock`, `stock_status`, `created_at`, `updated_at` are informational (ignored on import).

**CSV conventions (both exports):** UTF-8 **with BOM** (decision: admins open the files in Excel with Indonesian regional settings, which garbles BOM-less UTF-8), comma delimiter, CRLF, RFC 4180 quoting. Text cells starting with `=`, `+`, `-`, `@`, TAB or CR are prefixed with `'` (formula-injection safety); numeric columns are never prefixed. Errors before the first byte return JSON; a failure mid-stream truncates the file (status already 200), so compare the row count with the list total. Download via `fetch` + blob (bearer token), not a bare link. Excel with an Indonesian list separator (`;`) opens a double-clicked comma file in one column; tell admins to use **Data > From Text/CSV** (delimiter: comma). The import accepts either `,` or `;`, so a file re-saved by Excel with `;` imports fine.

## 6.25 Import Products / Stock (CSV, dry run)

```http
POST /api/v1/admin/products/import?type=products&dryRun=true
Content-Type: multipart/form-data   (field "file")
```

- `type` — `products` or `stock` (required). `dryRun` — default `true`; send `false` to apply.
- File ≤ 5 MiB, ≤ 5000 data rows, CSV only (an `.xlsx` is `400 UNSUPPORTED_FILE`; non-UTF-8 is `400 INVALID_ENCODING`). This route is exempt from the global 1 MiB request-body cap (`SECURITY_BODY_LIMIT_BYTES`); the handler caps the multipart body at 5 MiB + 64 KiB and answers `413 PAYLOAD_TOO_LARGE` as JSON. Held in memory only; contents are never logged.
- Optional BOM; `,` or `;` delimiter detected on the header line; headers lower-case, any order. Unknown / duplicate / missing required header → `400 INVALID_HEADER | DUPLICATE_HEADER | MISSING_HEADER` with `details.header`; zero writes.
- `type=products`: required `sku`; optional `variant_id, product_id, title, slug, status, price, compare_at, variant_price, stock, stock_baseline, low_stock_threshold, category_slug, brand_slug, description, attributes`; ignored `in_stock, stock_status, created_at, updated_at` (so an export re-imports as is).
- `type=stock`: required `sku, stock`; optional `variant_id, stock_baseline`. Never creates anything; unknown keys are `UNKNOWN_SKU` / `UNKNOWN_VARIANT`.

**Row identity** (resolved for every row before anything is written):

1. `variant_id` present → that variant (`update`). A non-empty `sku` that differs from the stored SKU is `SKU_MISMATCH` (catches Excel-mangled SKUs such as `1.23E+12` instead of creating a duplicate); a variant without SKU gets the row's `sku` assigned (`type=products` only).
2. else `sku` found in this store → `update`.
3. else (unknown `sku`): with `product_id` → `create_variant` on that product; without → rows sharing the same `slug` (or slugified `title`) create **one** new product (`create`; every row of the group reports the same `slug`), unless that slug already names a product of this store (`create_variant`). A derived slug taken by another store gets `-2`…`-20`; an explicit taken slug is `SLUG_TAKEN`; a SKU used by another store is `SKU_TAKEN`.
4. `sku` and `variant_id` empty but `product_id` present → product-level update only (this is how products without variants export); a non-empty variant column on that row is `NO_VARIANT`.
5. nothing → `MISSING`.

A row carrying `variant_id` or `product_id` never creates a product. Keys must be unique in the file (`DUPLICATE_SKU`, `DUPLICATE_VARIANT`). Rows of one product must carry identical product-level cells (`CONFLICTING_PRODUCT_VALUES`); product-level changes appear on the product's first row.

**Empty cells:** `compare_at`, `low_stock_threshold`, `description`, `category_slug`, `brand_slug` — empty clears. Every other column — empty leaves the value unchanged (create defaults: status `published`, stock `0`, variant price = product price, attributes `{}`; `title` and `price` are required to create: `MISSING_FOR_CREATE`). `0` is a value, never "empty". A `'` before a formula trigger is removed, so `'=X` stores `=X` (text that literally starts with `'` plus a trigger cannot be stored through an import). `title` and `description` are compared ignoring CR characters (a CSV round trip turns CRLF into LF and drops a lone CR), so an unchanged export re-imports as `unchanged`.

**Prices:** `price` is the product (listing) price only; `variant_price` is what the cart charges. Editing only `price` leaves variant prices as they are, so listing and cart disagree — edit `variant_price` on every row of that product too (or use bulk price with `applyTo=both`).

**Stock and sales since the export** (no silent overselling):

| File `stock` vs `stock_baseline` | Live stock | Result |
|---|---|---|
| equal (cell untouched) | anything | no change |
| different | equals baseline | stock set to the file value |
| different | different from baseline and file | row rejected `STALE_STOCK` |

Without `stock_baseline`: `type=stock` sets absolute counts (its purpose); `type=products` refuses to change the stock of an existing variant (`STOCK_BASELINE_REQUIRED`). Stock always applies on create.

**Response:** `200 OK` (dry run, applied, or a commit with nothing to change) / `422 VALIDATION_FAILED` (any rejected row; `error.details` has the same object; nothing written in either mode):

```json
{
  "data": {
    "dryRun": true,
    "type": "products",
    "summary": { "total": 120, "create": 2, "createVariant": 1, "update": 10, "unchanged": 105, "rejected": 2 },
    "rows": [
      { "row": 2, "sku": "ABC-1", "variantId": "v-uuid", "productId": "p-uuid", "slug": "sepatu", "action": "update",
        "changes": { "stock": { "from": 4, "to": 10 }, "variant_price": { "from": 10000, "to": 12000 } }, "errors": [] },
      { "row": 3, "sku": "NEW-1", "slug": "sepatu-lari-2", "action": "create",
        "changes": { "title": { "from": null, "to": "Sepatu Lari" }, "price": { "from": null, "to": 250000 } }, "errors": [] },
      { "row": 7, "sku": "", "action": "reject", "changes": {},
        "errors": [ { "field": "sku", "code": "MISSING", "message": "sku, variant_id or product_id is required" } ] }
    ]
  }
}
```

Actions: `create`, `create_variant`, `update`, `unchanged`, `reject`. `row` is the spreadsheet row (header = row 1; blank rows count). Row codes: `MISSING`, `INVALID_ROW`, `INVALID_NUMBER`, `INVALID_UUID`, `NEGATIVE`, `OUT_OF_RANGE`, `INVALID_STATUS`, `INVALID_JSON`, `INVALID_SLUG`, `DUPLICATE_SKU`, `DUPLICATE_VARIANT`, `SKU_TAKEN`, `SKU_MISMATCH`, `PRODUCT_MISMATCH`, `SLUG_TAKEN`, `UNKNOWN_SKU`, `UNKNOWN_VARIANT`, `UNKNOWN_PRODUCT`, `MISSING_FOR_CREATE`, `UNKNOWN_CATEGORY`, `UNKNOWN_BRAND`, `CONFLICTING_PRODUCT_VALUES`, `NO_VARIANT`, `STALE_STOCK`, `STOCK_BASELINE_REQUIRED`.

Other errors: `400` (bad `type`/`dryRun`, missing file, `UNSUPPORTED_FILE`, `INVALID_ENCODING`, `EMPTY_FILE`, `INVALID_CSV` with `details.row`, `TOO_MANY_ROWS`, header errors), `413 PAYLOAD_TOO_LARGE`, `409 CONFLICT` (lock timeout, deadlock or a unique slug/SKU race — retry).

The preview is not stored: a commit re-plans the file in the transaction that writes, so show the **commit** response as the source of truth. Re-importing an unchanged file reports every row `unchanged`, writes nothing, emits no events and writes no audit row. A committed import writes one `catalog.import.commit` audit row (`resource_type: catalog_import`, metadata `{type, summary, rows: [...changed rows with from/to]}`) in the same transaction; a rejected run (either mode) writes `catalog.import.rejected` (`{type, dryRun, summary, rejected}`).

## 6.26 Low Stock: Inventory Filter, Tenant Default, Overview

Classification (`internal/catalog/lowstock.go`): `out` at stock 0, `low` at 1..threshold, `in` above. Per variant on the inventory list, per product (sum of variants) on the product list, export and overview; a product without variants follows its `inStock` flag. Threshold = product `lowStockThreshold`, else the tenant default, else `5`; `0` never reports low.

```http
GET /api/v1/admin/inventory?stockStatus=low      (or ?lowStock=true)
```

Items gain `stockStatus` and `lowStockThreshold` (effective):

```json
{ "data": [ { "variantId": "v", "productId": "p", "sku": "A-1", "productTitle": "Topi", "price": 1000, "stock": 3, "stockStatus": "low", "lowStockThreshold": 5 } ] }
```

Tenant default: `GET /api/v1/admin/settings` → `data.inventory.lowStockThreshold` (absent = 5); `PATCH /api/v1/admin/settings` with `{"inventory": {"lowStockThreshold": 10}}` (integer 0..1000000, else 400).

Overview: `GET /api/v1/admin/analytics/overview` adds `lowStockProducts` and `outOfStockProducts` (all other fields unchanged).

## 6.27 Export Orders (CSV)

```http
GET /api/v1/admin/orders/export?status=PAID&search=&startDate=2026-03-01&endDate=2026-04-01
```

One row per order, newest first, same filters as `GET /admin/orders`. Dates are UTC: `startDate` inclusive, `endDate` **exclusive** (convert the user's local range before sending). Filename `orders-YYYYMMDD-HHMMSS.csv`; CSV conventions as 6.24.

Columns: `order_number, order_id, status, payment_status, customer_name, customer_email, currency, subtotal, discount, tax, shipping, total, voucher_code, items_count, courier, tracking_number, created_at, updated_at`.

## 6.28 Product Domain Events (Webhooks)

Topics `product.price_changed` (fields `price`, `compare_at`; variant price changes carry `variantId`/`sku`), `product.stock_changed`, `product.status_changed`. Payload:

```json
{ "tenantId": "t", "productId": "p", "slug": "sepatu", "variantId": "v", "sku": "A-1", "field": "stock", "old": 10, "new": 7,
  "source": "single|bulk|import", "requestId": "req-id", "actorUserId": "u" }
```

Emitted after commit by single updates (`PATCH /admin/products/{id}`, `PATCH /admin/products/{id}/stock`, `PATCH /admin/inventory/{variantId}`), bulk and import. Events are stamped with the tenant and delivered **only to that tenant's endpoints** (topic-subscribed or catch-all). Order, payment and shipment events are not tenant-stamped yet and keep broadcasting to every active endpoint subscribed to the topic (unchanged behaviour). Payloads never contain customer data.
