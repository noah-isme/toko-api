# Product Alerts (Back in Stock / Price Drop)

> **Last Updated:** 2026-10-25

Signed-in customers can subscribe to a product, or to one variant of it, and get
told once when it becomes available again (`BACK_IN_STOCK`) or when its price
drops below the price at subscribe time (`PRICE_DROP`).

All endpoints require a Bearer token and an active membership in the tenant
(`X-Tenant-ID` resolved as usual). Every read and write is scoped to the caller
**and** the active tenant: another user's or another tenant's subscription
behaves as if it does not exist (`404`).

Base path: `/api/v1/alerts`

Money: every price field is in **whole currency units** (rupiah), exactly like
`products.price` and the cart/order responses. Format it with the existing
`formatCurrency`. Do not divide it by 100.

## AlertSubscription object

```json
{
  "id": "6f0c2f3e-0000-4000-8000-000000000001",
  "productId": "0d9e6a52-0000-4000-8000-000000000002",
  "variantId": "a1b2c3d4-0000-4000-8000-000000000003",
  "type": "PRICE_DROP",
  "priceAtSubscribe": 1250000,
  "status": "active",
  "notifiedAt": null,
  "createdAt": "2026-10-25T03:00:00Z",
  "product": {
    "title": "Kaos Hitam",
    "slug": "kaos-hitam",
    "thumbnail": "https://cdn.example.com/kaos.jpg",
    "price": 1250000,
    "inStock": true
  },
  "variant": { "id": "a1b2c3d4-0000-4000-8000-000000000003", "sku": "KH-M", "price": 1250000, "stock": 0 }
}
```

| Field              | Type                         | Notes |
|--------------------|------------------------------|-------|
| `id`               | string (uuid)                | Subscription id (used for `DELETE`). |
| `productId`        | string (uuid)                | |
| `variantId`        | string (uuid) \| null        | `null` for a product-level subscription. |
| `type`             | `BACK_IN_STOCK` \| `PRICE_DROP` | |
| `priceAtSubscribe` | integer \| null              | Effective price when subscribed (variant price if `variantId` is set, else product price). Always `null` for `BACK_IN_STOCK`. |
| `status`           | `active` \| `notified`       | `notified` once the alert has fired. It fires again only after a re-subscribe. |
| `notifiedAt`       | string (RFC 3339) \| null    | When the alert fired. |
| `createdAt`        | string (RFC 3339)            | First subscribe time. A re-arm does not change it. |
| `product`          | object                       | Current snapshot: `title`, `slug`, `thumbnail` (nullable), `price`, `inStock` (the product-level flag). |
| `variant`          | object                       | **Omitted** when `variantId` is null. `id`, `sku` (nullable), `price`, `stock`. |

---

## List my alerts

```http
GET /api/v1/alerts?page=1&limit=20&productId=<uuid>
Authorization: Bearer <token>
```

| Query       | Default | Notes |
|-------------|---------|-------|
| `page`      | `1`     | 1-based. |
| `limit`     | `20`    | Clamped to `100`. |
| `productId` | (none)  | Optional filter to one product, for example to render the "notify me" toggle on a PDP. |

Sorted by `createdAt` descending.

**Response:** `200 OK`
```json
{
  "data": [ { "...": "AlertSubscription" } ],
  "pagination": { "page": 1, "perPage": 20, "totalItems": 1 }
}
```

Errors: `400 BAD_REQUEST` (invalid `productId`), `401 UNAUTHORIZED`.

---

## Subscribe / re-arm

```http
POST /api/v1/alerts
Authorization: Bearer <token>
Content-Type: application/json

{ "productId": "uuid", "variantId": "uuid (optional)", "type": "BACK_IN_STOCK" }
```

- `type` is case-insensitive (`back_in_stock` is accepted) and is returned upper-case.
- One subscription exists per (user, product, variant-or-none, type). Posting
  again for the same target **re-arms** it: `status` goes back to `active`,
  `notifiedAt` becomes `null`, and for `PRICE_DROP` `priceAtSubscribe` is set to
  the current price.

**Response:** `201 Created` for a new subscription, `200 OK` for a re-arm. Both
return `{ "data": AlertSubscription }`.

| Status | `error.code`       | When |
|--------|--------------------|------|
| 400    | `BAD_REQUEST`      | Invalid JSON, invalid `productId`/`variantId`, or `type` not `BACK_IN_STOCK`/`PRICE_DROP`. |
| 401    | `UNAUTHORIZED`     | No/invalid token. |
| 404    | `NOT_FOUND`        | Product not in this tenant, or `variantId` is not a variant of `productId`. |
| 409    | `ALREADY_IN_STOCK` | `BACK_IN_STOCK` requested while the target can already be bought. Hide the "notify me" button when the product or variant is sellable. |

"Sellable" uses the same rule as cart/checkout. A variant is sellable when the
product's `inStock` flag is on and the variant `stock > 0`. A product is
sellable when `inStock` is on and it either has no variants or at least one
variant with `stock > 0`. `PRICE_DROP` can be created at any time.

---

## Unsubscribe

```http
DELETE /api/v1/alerts/{id}
Authorization: Bearer <token>
```

**Response:** `204 No Content`.
Errors: `400 BAD_REQUEST` (invalid id), `404 NOT_FOUND` (no such subscription for this user in this tenant).

---

## When alerts fire

- **Back in stock** fires on a transition from not sellable to sellable. Triggers:
  admin stock edits (`PATCH /admin/products/{id}/stock`,
  `PATCH /admin/inventory/{variantId}`), admin product edits that set
  `inStock` or variant stock, an order cancellation, and a failed or expired
  payment that releases reserved stock. The current state is checked again at
  delivery time, so only subscriptions whose target is sellable *at that moment*
  are notified. The others stay `active`.
- **Price drop** fires when the product price (product-level subscription) or the
  variant price (variant subscription) decreases **and** the new price is below
  `priceAtSubscribe`. A decrease that is still at or above the subscribed price
  does not fire.
- Each subscription fires **at most once** until re-armed. It is enforced in the
  database, including with several API replicas.
- Timing: a background drainer in the API processes changes every 15s, and
  immediately after admin stock/price edits and order cancels. Expect the
  notification within about 15s.
- Preferences: if the user's push preferences have `enabled = false` or
  `types.stock_updates = false` (`PATCH /api/v1/push/preferences`), the
  subscription is still consumed (`status: notified`) but **no** in-app
  notification or email is produced. A user without saved preferences is
  treated as opted in.

## Delivery

1. **In-app notification** (existing `GET /api/v1/notifications`):

   | `type`          | `title`                 | `body` (example) |
   |-----------------|-------------------------|------------------|
   | `back_in_stock` | `Stok kembali tersedia` | `Kaos Hitam sudah tersedia kembali.` |
   | `price_drop`    | `Harga turun`           | `Kaos Hitam turun dari Rp 1.250.000 menjadi Rp 999.000.` |

   `data`:
   ```json
   {
     "productId": "uuid", "variantId": "uuid | null", "slug": "kaos-hitam", "title": "Kaos Hitam",
     "price": 999000, "previousPrice": 1250000, "topic": "alert.price_drop", "subscriptionId": "uuid"
   }
   ```
   `previousPrice` (the subscribed price) is present only for `price_drop`. Use
   `slug` to link to the product page.

2. **Email** goes through the existing email notifier on domain topics
   `alert.back_in_stock` and `alert.price_drop`. Subjects are `Stok kembali tersedia` and `Harga turun`,
   and the body includes the message above. Each topic can be switched off with
   `NOTIFY_EMAIL_TOPIC_ALERT_BACK_IN_STOCK=false` and
   `NOTIFY_EMAIL_TOPIC_ALERT_PRICE_DROP=false`. The domain event row is written
   in the same transaction as the in-app notification. Its payload is
   `{ subscriptionId, userId, email, tenantId, productId, variantId, productSlug, productTitle, price, previousPrice?, message }`.

3. **Webhooks:** `alert.*` topics are **not** delivered to webhook endpoints,
   including catch-all endpoints with an empty `topics` list. Do not offer them in
   webhook topic pickers.

Delivery semantics: the in-app notification, the `notified` state and the
domain event commit atomically. Email is sent after commit with a per-message
timeout. If the API process stops mid-send, the email for that alert can be
lost, but the in-app notification and the domain event row remain, and the
user is never notified twice.
