# Analytics Endpoints

All analytics endpoints require **admin authentication**.

## 9.1 Get Sales Analytics

```http
GET /api/v1/analytics/sales?from=2025-11-01T00:00:00Z&to=2025-12-01T00:00:00Z
Authorization: Bearer <admin_token>
```

Returns daily sales aggregates for the requested date range. The range is inclusive of `from` and exclusive of `to`.

**Query parameters:**

- `from` — start date as **RFC3339** (e.g. `2025-11-01T00:00:00Z`)
- `to` — end date as **RFC3339** (e.g. `2025-12-01T00:00:00Z`)
- `days` — number of days to look back when `from`/`to` are omitted. Defaults to the server's configured analytics range (fallback `30`)

When both `from` and `to` are provided, `days` is ignored. When omitted, `to` is set to now and `from` is `to - days`.

**Response:** `200 OK`

```json
{
  "data": [
    {
      "day": "2025-11-15T00:00:00Z",
      "paid_orders": 12,
      "all_orders": 15,
      "revenue": 1250000
    }
  ]
}
```

- `day` — timestamp for the aggregation day
- `paid_orders` — orders that have been paid in the period
- `all_orders` — total orders in the period (including unpaid / failed)
- `revenue` — gross revenue from paid orders in IDR

**Error Response:**

```json
{
  "error": {
    "code": "BAD_REQUEST",
    "message": "invalid from date"
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `BAD_REQUEST`, `ANALYTICS_NOT_CONFIGURED`, `ANALYTICS_ERROR`.

---

## 9.2 Get Top Products

```http
GET /api/v1/analytics/top-products?limit=10&offset=0
Authorization: Bearer <admin_token>
```

Returns the top-selling products ordered by quantity sold.

**Query parameters:**

- `limit` — number of items to return (default: `10`, minimum: `1`)
- `offset` — number of items to skip (default: `0`)

**Response:** `200 OK`

```json
{
  "data": [
    {
      "product_id": "product-uuid",
      "qty_sold": 150,
      "gross": 15000000
    }
  ]
}
```

- `product_id` — UUID of the product
- `qty_sold` — total units sold across completed orders
- `gross` — gross revenue for the product in IDR

**Error Response:**

```json
{
  "error": {
    "code": "ANALYTICS_ERROR",
    "message": "..."
  }
}
```

Common error codes: `UNAUTHORIZED`, `FORBIDDEN`, `ANALYTICS_NOT_CONFIGURED`, `ANALYTICS_ERROR`.

---

## 9.3 Overview

```http
GET /api/v1/analytics/overview
Authorization: Bearer <admin_token>
```

Returns a high-level dashboard summary for the requested range. Use `days` (default 30) or explicit `from` and `to` RFC3339 query parameters.

**Response:** `200 OK`

```json
{
  "data": {
    "from": "2026-07-03T00:00:00Z",
    "to": "2026-08-02T00:00:00Z",
    "totalRevenue": 12500000,
    "totalOrders": 42,
    "paidOrders": 38,
    "averageOrderValue": 328947
  }
}
```
