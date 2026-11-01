# Analytics Endpoints

> **Last Updated:** 2026-10-25

All analytics and report endpoints require **admin authentication** (`Authorization: Bearer <admin_token>`, role `admin`, tenant membership) and are recorded in the audit log.

## Tenant scope

Every endpoint in this document returns data for the **current tenant only** (resolved from `X-Tenant-ID` / subdomain by the tenant middleware). A request without a tenant, or for a tenant that does not exist, gets `400 TENANT_REQUIRED`. The legacy `/api/v1/analytics/*` endpoints used to read global materialized views (every tenant's orders); since migration `000042` they run live tenant-scoped queries.

## Revenue rule

One rule backs every revenue figure (`/analytics/sales`, `/analytics/top-products`, `/analytics/overview`, `/admin/analytics/overview`, `/admin/orders/stats`, `/admin/reports/*`):

1. **Revenue statuses:** `PAID`, `PACKED`, `SHIPPED`, `OUT_FOR_DELIVERY`, `DELIVERED`. `PENDING_PAYMENT` and `CANCELLED` never count (a failed/expired payment moves the order to `CANCELLED`; there is no order-level `REFUNDED`/`EXPIRED` status).
2. **Refunds** come from the `refunds` table. `refunded(order)` = sum of `refunds.amount` with status `PENDING` or `SUCCEEDED`. `FAILED` refunds are ignored. `PENDING` refunds **are subtracted**: the refund flow records a `PENDING` row before calling the payment gateway and leaves indeterminate outcomes `PENDING`, so the money is treated as leaving; if the refund later fails, the revenue comes back.
3. An order is a **revenue order** iff it has a revenue status and is **not fully refunded** (`refunded < pricing_total`, or no refund at all). Partially refunded orders stay revenue orders.
4. **Order-level amounts are net:** `pricing_total - refunded`. Used by `totalRevenue`, daily `revenue`, `averageOrderValue`, conversion `paidRevenue`, and all customer revenue figures. `averageOrderValue = totalRevenue / revenueOrders` (rounded; `0` when there are none).
5. **Item-level amounts are gross:** `SUM(order_items.subtotal)` of revenue orders, before order-level discount/shipping/tax and **not reduced by partial refunds** (refunds are not allocated to items). Fully refunded orders are excluded. Used by top products, sales by category and sales by product. Item-level totals therefore do **not** have to equal order-level revenue.
6. Orders are attributed to the time of `orders.created_at` (there is no `paid_at`).

`totalOrders` / `all_orders` / `checkoutsStarted` and the per-status counters (`pendingOrders`, `paidOrders` on the admin stats, ...) still count **every** order in range regardless of the rule.

Example: a 1,000,000 IDR delivered order with a 5,000 IDR partial refund contributes 995,000 to `totalRevenue`, 1 to `revenueOrders`, its full item subtotals to the category/product reports, and `5000` to `refundedAmount`. The same order fully refunded contributes 0 revenue, is excluded from the item reports and counts once in `refundedOrders`.

## Timezone

- Day buckets and date-only bounds use the tenant's `tenants.timezone` (IANA name, e.g. `Asia/Jakarta`). An empty or invalid value falls back to `UTC` (logged as a warning); the reports echo the zone actually used in `timezone`.
- `/admin/reports/*`: `from`/`to` accept RFC3339 (used as-is) or `YYYY-MM-DD` = **midnight in the tenant timezone**. `day` values are `YYYY-MM-DD` tenant-local dates.
- `/analytics/sales`: `day` is the tenant-local midnight rendered **with the tenant offset**, e.g. `2026-10-01T00:00:00+07:00` (previously always `...T00:00:00Z`). When `from`/`to` are omitted, the `days` window ends at the next tenant-local midnight.
- `/admin/orders/stats` keeps its existing parsing: a date-only `startDate`/`endDate` is **UTC** midnight. For a WIB tenant `startDate=2026-10-01` therefore starts at 07:00 local time, and the same dates give different totals than `/admin/reports/*`. To get tenant-day totals from it, pass RFC3339 bounds with the offset (`startDate=2026-10-01T00:00:00+07:00`).
- `/admin/analytics/overview` uses rolling windows (`now - N days`), not day boundaries.

## Common errors

`{ "error": { "code", "message" } }` with codes `UNAUTHORIZED` (401), `FORBIDDEN` (403), `BAD_REQUEST` (400), `TENANT_REQUIRED` (400), `INTERNAL` / `ANALYTICS_ERROR` / `ANALYTICS_NOT_CONFIGURED` (500).

---

## 9.1 Get Sales Analytics

```http
GET /api/v1/analytics/sales?from=2026-09-30T17:00:00Z&to=2026-10-02T17:00:00Z
```

Daily sales for `[from, to)`, one row per tenant-local day that has orders (days without orders are omitted).

**Query parameters:**

- `from`, `to` — RFC3339. Used only when **both** are given.
- `days` — number of whole tenant-local days ending at the next local midnight, used when `from`/`to` are omitted. Defaults to the server's configured analytics range (fallback `30`).

**Response:** `200 OK`

```json
{
  "data": [
    { "day": "2026-10-01T00:00:00+07:00", "paid_orders": 2, "all_orders": 3, "revenue": 285000 }
  ]
}
```

- `day` — tenant-local midnight with offset
- `paid_orders` — revenue orders (all revenue statuses, minus fully refunded orders; previously only `PAID`)
- `all_orders` — every order created that day
- `revenue` — net revenue (IDR) of the revenue orders

Responses are cached in Redis per tenant, timezone and exact `from`/`to` instants for the configured TTL; the cache is cleared on payment webhooks and voucher changes.

---

## 9.2 Get Top Products

```http
GET /api/v1/analytics/top-products?limit=10&offset=0&from=2026-10-01T00:00:00+07:00&to=2026-11-01T00:00:00+07:00
```

The tenant's top-selling products ordered by quantity sold (ties by product id).

**Query parameters:**

- `limit` — default `10`, clamped to `1..100`
- `offset` — default `0`
- `from`, `to` — optional RFC3339 bounds on order `created_at` (`[from, to)`); each may be given alone; without them the result is all-time. `from >= to` is `400`.

**Response:** `200 OK`

```json
{ "data": [ { "product_id": "uuid", "qty_sold": 150, "gross": 15000000 } ] }
```

- `qty_sold` — units in revenue orders
- `gross` — item-level gross (`order_items.subtotal`) in IDR

---

## 9.3 Overview

```http
GET /api/v1/analytics/overview?days=30
```

Sum of the 9.1 rows for the window. Use `days` (default 30, whole tenant-local days) or explicit RFC3339 `from` / `to` (each overrides its side of the default window).

```json
{
  "data": {
    "from": "2026-09-26T00:00:00+07:00",
    "to": "2026-10-26T00:00:00+07:00",
    "totalRevenue": 12500000,
    "totalOrders": 42,
    "paidOrders": 38,
    "averageOrderValue": 328947
  }
}
```

`paidOrders` = revenue orders, `totalRevenue` = net revenue, `averageOrderValue = totalRevenue / paidOrders` (truncated).

---

## 9.4 Admin dashboard overview and order stats

These two existing endpoints keep their paths and keys; their revenue numbers now follow the revenue rule, and both gain `revenueOrders`.

```http
GET /api/v1/admin/analytics/overview?range=30d      # 7d | 30d | 90d | all, rolling window
GET /api/v1/admin/orders/stats?startDate=2026-10-01T00:00:00+07:00&endDate=2026-11-01T00:00:00+07:00
```

Changed fields (both responses):

- `totalRevenue` — net revenue of revenue orders. Previously the sum of `pricing_total` over **all** statuses (orders stats) / non-cancelled orders; expect lower numbers when pending, cancelled or refunded orders exist.
- `revenueOrders` — **new**: number of revenue orders.
- `averageOrderValue` — `totalRevenue / revenueOrders` (rounded). Previously the average over all orders.
- `totalOrders` and `pendingOrders`/`paidOrders`/`shippedOrders`/`deliveredOrders`/`cancelledOrders` are unchanged (every order, by status).
- Overview `topProducts` — item-level gross of revenue orders (previously any non-cancelled order, including pending and refunded).

`/admin/orders/stats` honors both `startDate` (inclusive) and `endDate` (exclusive); see **Timezone** for the UTC-midnight caveat on date-only values.

---

## Admin reports (9.5 - 9.8)

```http
GET /api/v1/admin/reports/{report}?from=2026-10-01&to=2026-11-01[&format=csv]
```

Common query parameters:

- `from` — inclusive start: RFC3339, or `YYYY-MM-DD` = midnight in the tenant timezone. Default: 30 days before `to`.
- `to` — exclusive end, same formats. Default: the next tenant-local midnight (so the default range is the last 30 whole local days including today).
- `from < to` and `to - from <= 366 days`, otherwise `400 BAD_REQUEST`.
- `format` — `json` (default) or `csv`; anything else is `400`.

Every JSON report echoes the resolved `from` / `to` (RFC3339 in the tenant offset) and `timezone`. Money is integer IDR; rates are fractions rounded to 4 decimals. Empty ranges return `200` with zeros and empty arrays.

**CSV (`format=csv`):** `200`, `Content-Type: text/csv; charset=utf-8`, `Content-Disposition: attachment; filename="<report>_<from>_<to>.csv"` (tenant-local dates of the resolved range, e.g. `sales-by-category_2026-10-01_2026-11-01.csv`), `Cache-Control: no-store`. The body starts with a UTF-8 BOM, uses comma separators and RFC 4180 quoting. **Formula-injection safety:** every text cell (category/product names, slugs, titles) that starts with `=`, `+`, `-`, `@`, TAB or CR is prefixed with `'`; numbers, UUIDs and dates are written raw. Rows are streamed; a database error after the first row has been sent truncates the file (logged) because the status is already `200`. Errors before the first byte (bad parameters, failing first query) return the normal JSON error.

### 9.5 Sales by category

```http
GET /api/v1/admin/reports/sales-by-category
```

```json
{
  "data": {
    "from": "2026-10-01T00:00:00+07:00", "to": "2026-10-03T00:00:00+07:00", "timezone": "Asia/Jakarta",
    "totalRevenue": 500000, "totalUnits": 5,
    "rows": [
      { "categoryId": "uuid", "categoryName": "Laptop", "categorySlug": "laptop", "unitsSold": 3, "orders": 2, "revenue": 300000, "share": 0.6 },
      { "categoryId": null, "categoryName": "Uncategorized", "categorySlug": null, "unitsSold": 2, "orders": 2, "revenue": 200000, "share": 0.4 }
    ]
  }
}
```

Item-level gross of revenue orders, grouped by the product's current category (products without one, or deleted products, are `Uncategorized` with `categoryId: null`). `orders` = distinct orders containing the category. `share = revenue / totalRevenue`. Sorted by revenue desc, then name.

CSV columns: `category_id,category,category_slug,units_sold,orders,revenue,share`.

### 9.6 Sales by product (drill-down)

```http
GET /api/v1/admin/reports/sales-by-product?page=1&limit=20&categoryId=<uuid>
```

- `page` (default 1), `limit` (default 20, max 100)
- `categoryId` — optional; only products in that category (drill-down from 9.5). Not a UUID → `400 BAD_REQUEST`.

```json
{
  "data": [
    { "productId": "uuid", "title": "Laptop Pro", "slug": "laptop-pro", "categoryId": "uuid|null", "categoryName": "Laptop",
      "unitsSold": 3, "orders": 2, "revenue": 300000 }
  ],
  "pagination": { "page": 1, "perPage": 20, "totalItems": 2 },
  "range": { "from": "2026-10-01T00:00:00+07:00", "to": "2026-10-03T00:00:00+07:00", "timezone": "Asia/Jakarta" }
}
```

Same rule and amount basis as 9.5; `title`/`slug` are taken from the order items. Sorted by revenue desc, then product id. There is no `share` (it would need the unpaginated total; use 9.5).

CSV ignores `page`/`limit` and streams the whole result (filters still apply), up to 100,000 rows: `product_id,title,slug,category_id,category,units_sold,orders,revenue`.

### 9.7 Conversion

```http
GET /api/v1/admin/reports/conversion
```

```json
{
  "data": {
    "from": "...", "to": "...", "timezone": "Asia/Jakarta",
    "cartsCreated": 3, "checkoutsStarted": 7, "paidOrders": 4, "pendingOrders": 1, "cancelledOrders": 1, "refundedOrders": 1,
    "paidRevenue": 485000, "refundedAmount": 1014000,
    "checkoutRate": 2.3333, "paymentRate": 0.5714, "conversionRate": 1.3333
  }
}
```

Definitions (built from existing data; there is no checkout-session table):

- `cartsCreated` — carts (signed-in and anonymous) created in range
- `checkoutsStarted` — orders created in range (every `POST /checkout` creates one `PENDING_PAYMENT` order)
- `paidOrders` — revenue orders (includes partially refunded ones)
- `pendingOrders` — `PENDING_PAYMENT`; `cancelledOrders` — `CANCELLED`
- `refundedOrders` — revenue-status orders that are **fully** refunded
- `checkoutsStarted = paidOrders + pendingOrders + cancelledOrders + refundedOrders`
- `paidRevenue` — net revenue of `paidOrders`
- `refundedAmount` — `PENDING` + `SUCCEEDED` refunds recorded against orders created in range
- `checkoutRate = checkoutsStarted / cartsCreated`, `paymentRate = paidOrders / checkoutsStarted`, `conversionRate = paidOrders / cartsCreated`; `0` when the denominator is `0`

This is an approximation: a cart can be checked out several times (rates above 1 are possible), and carts that were abandoned before checkout are included.

CSV: one data row, `from,to,timezone,carts_created,checkouts_started,paid_orders,pending_orders,cancelled_orders,refunded_orders,paid_revenue,refunded_amount,checkout_rate,payment_rate,conversion_rate`.

### 9.8 Customers (new vs returning)

```http
GET /api/v1/admin/reports/customers
```

```json
{
  "data": {
    "from": "...", "to": "...", "timezone": "Asia/Jakarta",
    "newCustomers": 2, "returningCustomers": 0, "totalCustomers": 2, "repeatRate": 0,
    "ordersFromNew": 3, "ordersFromReturning": 0, "revenueFromNew": 435000, "revenueFromReturning": 0,
    "guestOrders": 1, "guestRevenue": 50000,
    "daily": [ { "day": "2026-10-01", "newCustomers": 2, "returningCustomers": 0, "orders": 2, "revenue": 285000 } ]
  }
}
```

- A **customer** is a signed-in user with at least one revenue order in range. Their first order is their earliest revenue order **at this tenant** (orders at other tenants never count).
- **Summary:** a customer is `new` iff their first order is at or after `from`, otherwise `returning`; each user is counted once. `totalCustomers = new + returning`, `repeatRate = returningCustomers / totalCustomers`. `ordersFrom*` / `revenueFrom*` are their revenue orders / net revenue in range.
- `guestOrders` / `guestRevenue` — revenue orders without a user in range. `revenueFromNew + revenueFromReturning + guestRevenue` equals the range's net revenue (`paidRevenue` of 9.7).
- **daily** (tenant-local days, customer orders only, no guests): `newCustomers` = users whose first order falls on that day, `returningCustomers` = users ordering that day whose first order was on an earlier day. A user who is "new" in the summary can be "returning" on a later day of the same range, so daily rows do not sum to the summary.

CSV = the `daily` series only: `day,new_customers,returning_customers,orders,revenue`.
