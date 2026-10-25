# Admin Endpoints

> **Last Updated:** 2026-10-25

All admin endpoints require a bearer token, a resolved tenant (`X-Tenant-ID` or
subdomain), an ACTIVE membership in that tenant with a **staff role**, and the
endpoint's **permission** (see [Permissions per endpoint](#permissions-per-endpoint)
and [auth.md 1.12](auth.md#112-roles--permissions)). A missing permission
returns `403 FORBIDDEN` with `details.required` naming it:

```json
{ "error": { "code": "FORBIDDEN", "message": "insufficient permissions", "details": { "required": "orders:write" } } }
```

`details.required` is `"staff"` for a member whose role is `customer` and
`"platform-admin"` for the platform-global endpoints. Reading, changing or
deleting a resource of another tenant by id or code returns `404`, never `403`,
a product cannot reference another tenant's brand or category, and a category
cannot have another tenant's category as `parentId` (`400`).

**Known gap (open, not yet resolved):** voucher codes, product slugs, category
slugs, brand names and slugs, and variant SKUs are still unique across **all**
tenants (database-wide unique constraints). Creating or renaming one of these
to a value another tenant already uses returns `409 CONFLICT`, which reveals
that the value exists somewhere on the platform and lets one tenant reserve a
value for everyone. Per-tenant uniqueness needs schema migrations that are not
part of this change; see ADR-011, Consequences.

Every `/api/v1/admin/*` request from an ACTIVE member of the tenant is audited
with `resource_type = "admin"`, the route, actor and final status, including
requests denied with `403 FORBIDDEN` (staff, platform-admin or permission
denials, for example a customer probing `/admin`). Requests rejected before
that (`401 UNAUTHORIZED`, `403 TENANT_FORBIDDEN`) are not audited. Mutations are
never dropped by audit sampling (only GET/HEAD/OPTIONS can be sampled); staff
changes also record `metadata.targetUserId`, `email`, `previousRole` and
`newRole`. `/api/v1/analytics/*` (read-only) is not audited.

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

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `CONFLICT` (duplicate code; codes are currently unique across all tenants, so this can also mean another tenant uses the code), `INTERNAL`.

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

## 6.20 List Roles

```http
GET /api/v1/admin/roles
Authorization: Bearer <token>
```

Permission: `staff:read`. Returns the seven staff roles in rank order with
their permissions (the server's matrix; render role pickers from it). `owner`
can be granted only by an owner; every other role by any holder of
`staff:manage`.

**Response:** `200 OK`

```json
{
  "data": [
    { "role": "owner", "permissions": ["analytics:read", "catalog:read", "..."] },
    { "role": "admin", "permissions": ["..."] },
    { "role": "manager", "permissions": ["..."] },
    { "role": "catalog", "permissions": ["catalog:read", "catalog:write", "inventory:read", "inventory:write", "promotions:read"] },
    { "role": "fulfillment", "permissions": ["..."] },
    { "role": "support", "permissions": ["..."] },
    { "role": "analyst", "permissions": ["..."] }
  ]
}
```

---

## 6.21 List Staff

```http
GET /api/v1/admin/staff?page=1&limit=20
Authorization: Bearer <token>
```

Permission: `staff:read`. Memberships of the current tenant whose role is not
`customer`, ordered by role rank (owner, admin, manager, then the rest) and
then email. `limit` is clamped to `100`.

**Response:** `200 OK`

```json
{
  "data": [
    {
      "userId": "user-uuid",
      "name": "Jane",
      "email": "jane@shop.id",
      "role": "manager",
      "status": "ACTIVE",
      "createdAt": "2026-10-01T00:00:00Z",
      "updatedAt": "2026-10-25T00:00:00Z"
    }
  ],
  "pagination": { "page": 1, "perPage": 20, "totalItems": 3 }
}
```

`status` is the membership status (`INVITED`, `ACTIVE`, `SUSPENDED`); only
ACTIVE members can use their role.

---

## 6.22 Assign Staff

```http
POST /api/v1/admin/staff
Content-Type: application/json
Authorization: Bearer <token>
```

Permission: `staff:manage`.

```json
{ "email": "jane@shop.id", "role": "manager" }
```

Grants a staff role to a user who is **already a member of this tenant** (they
registered on this tenant's storefront). The email lookup is case-insensitive
and limited to this tenant's members: an unknown email and an account that
only exists in another tenant both return `404 USER_NOT_FOUND`, and nobody is
enrolled into the tenant by this call. There is no invitation flow. A
`SUSPENDED` membership is reactivated.

**Response:** `201 Created` — `{ "data": StaffMember }` (shape as in 6.21).

**Errors:**

- `400 VALIDATION_ERROR` — invalid JSON, missing email, unknown role, or role `customer`.
- `403 FORBIDDEN` (`details.required = "staff:manage"`) — no `staff:manage`.
- `403 OWNER_REQUIRED` — role `owner` requested by a non-owner.
- `403 TENANT_FORBIDDEN` — the caller's own membership stopped being ACTIVE.
- `404 USER_NOT_FOUND` — no member of this tenant has that email.
- `409 STAFF_EXISTS` — the user is already staff; use 6.23.

---

## 6.23 Change Staff Role

```http
PATCH /api/v1/admin/staff/{userId}
Content-Type: application/json
Authorization: Bearer <token>
```

Permission: `staff:manage`.

```json
{ "role": "support" }
```

**Response:** `200 OK` — `{ "data": StaffMember }`.

**Errors:**

- `400 VALIDATION_ERROR` — `userId` is not a UUID, unknown role, or role `customer` (use 6.24).
- `403 FORBIDDEN` / `403 TENANT_FORBIDDEN` — as in 6.22.
- `403 OWNER_REQUIRED` — the target is an `owner`, or the new role is `owner`, and the caller is not an owner.
- `404 STAFF_NOT_FOUND` — the user has no staff membership in this tenant.
- `409 LAST_OWNER` — the target is the only ACTIVE owner. A `SUSPENDED` owner is never protected and never counted.

Callers may change their own role, subject to `LAST_OWNER`.

---

## 6.24 Revoke Staff

```http
DELETE /api/v1/admin/staff/{userId}
Authorization: Bearer <token>
```

Permission: `staff:manage`. Sets the membership role back to `customer`; the
membership itself is kept so the person keeps storefront access.

**Response:** `204 No Content`.

**Errors:** `400 VALIDATION_ERROR`, `403 FORBIDDEN`, `403 OWNER_REQUIRED`,
`403 TENANT_FORBIDDEN`, `404 STAFF_NOT_FOUND`, `409 LAST_OWNER` (as in 6.23).

Staff mutations (6.22-6.24) run in one transaction serialized per tenant, and
the caller's role is re-read inside it, so concurrent changes can never leave
a tenant without an ACTIVE owner or let a just-demoted caller act on a stale
role.

---

## Permissions per endpoint

`owner` and `admin` hold every tenant permission. Rows marked
**platform-admin** are outside the tenant matrix: they require the global
`users.roles` to contain `admin` (`403 FORBIDDEN`, `details.required =
"platform-admin"` otherwise), so no tenant role grants them.

| Endpoint | Permission |
|---|---|
| `GET /admin/products`, `GET /admin/products/{id}`, `GET /admin/categories`, `GET /admin/brands` | `catalog:read` |
| `POST /admin/products`, `PATCH/DELETE /admin/products/{id}`, `POST /admin/categories`, `PATCH/DELETE /admin/categories/{id}`, `POST /admin/brands`, `PATCH/DELETE /admin/brands/{id}` | `catalog:write` |
| `GET /admin/inventory` | `inventory:read` |
| `PATCH /admin/products/{id}/stock`, `PATCH /admin/inventory/{variantId}` | `inventory:write` |
| `GET /admin/orders`, `GET /admin/orders/stats`, `GET /admin/orders/{id}` | `orders:read` |
| `PATCH /admin/orders/{id}/status`, `POST /admin/orders/{id}/shipment` | `orders:write` |
| `GET /admin/returns` | `returns:read` |
| `PATCH /admin/returns/{returnId}` | `returns:write` |
| `POST /admin/returns/{returnId}/refund` | `refunds:write` |
| `GET /admin/customers`, `GET /admin/newsletter/subscribers` | `customers:read` |
| `GET /admin/support/tickets`, `GET /admin/support/tickets/{ticketId}/messages` | `support:read` |
| `PATCH /admin/support/tickets/{ticketId}`, `POST /admin/support/tickets/{ticketId}/messages` | `support:write` |
| `GET /admin/vouchers`, `GET /admin/vouchers/stats`, `POST /admin/vouchers/preview`, `GET /admin/flash-sales`, `GET /admin/flash-sales/{id}` | `promotions:read` |
| `POST /admin/vouchers`, `PUT /admin/vouchers/{code}`, `DELETE /admin/vouchers/{code}`, `POST /admin/flash-sales`, `PATCH /admin/flash-sales/{id}` | `promotions:write` |
| `GET /admin/analytics/overview` | `analytics:read` |
| `GET /admin/settings` | `settings:read` |
| `PATCH /admin/settings`, `POST /admin/onboarding` | `settings:write` |
| `GET /admin/roles`, `GET /admin/staff` | `staff:read` |
| `POST /admin/staff`, `PATCH /admin/staff/{userId}`, `DELETE /admin/staff/{userId}` | `staff:manage` |
| `POST/GET /admin/webhooks`, `PUT/DELETE /admin/webhooks/{id}`, `GET /admin/webhook-deliveries`, `POST /admin/webhook-deliveries/{id}/replay`, `GET /admin/queue/dlq`, `POST /admin/queue/dlq/replay`, `GET /admin/queue/stats`, `GET /admin/audit-logs` | **platform-admin** |
| `GET /analytics/sales`, `GET /analytics/top-products`, `GET /analytics/overview` | **platform-admin** (becomes `analytics:read` once the analytics endpoints are tenant-scoped) |

Each operation in `openapi.yaml` carries the same value as `x-permission`.

Voucher, order-status and shipment writes are tenant-scoped: a voucher code or
order id of another tenant returns `404`, and a voucher can only be redeemed
(cart, checkout, payment settlement) in the tenant that created it.

### Adding an admin route (backend)

Register every new `/api/v1/admin` or `/api/v1/analytics` route in
`cmd/api/routes_admin.go` with chi's `Method`, which accepts an
`http.Handler` (`Get`/`Post`/... only accept an `http.HandlerFunc` and will not
compile with a guard):

```go
a.Method(http.MethodGet, "/reports", rbac.Require(rbac.PermAnalyticsRead, d.Reports.List)) // tenant-scoped
p.Method(http.MethodGet, "/system", rbac.RequirePlatform(d.System.Get))                    // platform-global, inside the platform group
```

Pick a permission from the list above (extend the matrix in
`internal/rbac/roles.go` and this table if none fits). A route registered
any other way is still blocked for customers by the group-level
`rbac.RequireStaff`, and `TestAdminRoutesAreGuarded` (`cmd/api/routes_test.go`)
fails until it is wrapped and added to the test's expected-route table. Add
`x-permission` to its `openapi.yaml` operation.
