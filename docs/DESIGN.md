# Design Guide — Toko API

This document describes how the API should behave from the frontend's perspective: response shapes, error conventions, authentication flows, and UX-critical interaction patterns.

## API Conventions

### Base URL & Versioning

All endpoints are prefixed with `/api/v1`. No path contains a trailing slash.

### Response Envelope

Every successful response returns a JSON object with the domain data at the top level:

```json
{
  "id": "uuid",
  "name": "Product Name",
  "created_at": "2024-01-15T10:30:00Z"
}
```

List endpoints return an object with `data` (array) and `pagination`:

```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 142
  }
}
```

### Error Shape

All errors use a consistent shape:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Human-readable description",
    "details": { ... }
  }
}
```

Standard error codes: `VALIDATION_ERROR`, `NOT_FOUND`, `UNAUTHORIZED`, `FORBIDDEN`, `TENANT_FORBIDDEN`, `CONFLICT`, `RATE_LIMITED`, `INTERNAL`.

### HTTP Status Codes

| Status | Meaning |
|--------|---------|
| `200` | Success (GET, PATCH) |
| `201` | Created (POST) |
| `204` | No content (DELETE) |
| `400` | Validation error |
| `401` | Missing or invalid token |
| `403` | Authenticated but not authorized |
| `404` | Resource not found |
| `409` | Conflict (duplicate, idempotency) |
| `429` | Rate limited |
| `500` | Internal server error |

## Authentication Flow

```
┌─────────┐                         ┌─────────┐
│  Client  │                         │   API   │
└────┬────┘                         └────┬────┘
     │ POST /auth/register                │
     │───────────────────────────────────▶│
     │◀──────── 201 { user, tokens }──────│
     │                                    │
     │ POST /auth/login                   │
     │───────────────────────────────────▶│
     │◀──────── 200 { access_token }──────│
     │          Set-Cookie: refresh_token │
     │                                    │
     │ GET /api/v1/products               │
     │ Authorization: Bearer <access>     │
     │───────────────────────────────────▶│
     │◀──────── 200 { data }──────────────│
     │                                    │
     │ POST /auth/refresh                 │
     │ Cookie: refresh_token              │
     │───────────────────────────────────▶│
     │◀──────── 200 { access_token }──────│
     │          Set-Cookie: refresh_token │
     │                                    │
     │ POST /auth/logout                  │
     │───────────────────────────────────▶│
     │◀──────── 204 ─────────────────────│
```

- **Access token**: short-lived JWT in `Authorization: Bearer` header.
- **Refresh token**: `HttpOnly`, `Secure`, `SameSite=Lax` cookie. Rotated on every refresh.
- CSRF protection: mutating requests require `X-CSRF-Token` header.

## Cart & Checkout UX

### Anonymous Cart → Authenticated Merge

```
1. Shopper adds items (no login) → POST /carts, POST /carts/{id}/items
   - Cart stored with anon session token
2. Shopper logs in → POST /carts/merge
   - Server merges anon cart items into user's active cart
3. Shopper applies voucher → POST /carts/{id}/apply-voucher
4. Shopper requests shipping quote → POST /carts/{id}/quote/shipping
5. Shopper views draft → POST /checkout/draft
   - Returns itemised totals, shipping, tax, and voucher discount
6. Shopper confirms → POST /checkout
   - Atomic: reserve inventory → create order → create payment intent
   - Returns order ID and payment instructions
```

### Idempotency

Write endpoints under `/carts` and `/checkout` are wrapped with the `Idem` middleware. Clients should send an `Idempotency-Key` header. Replayed requests return the original response.

## Multi-Tenant UX

- Public catalog browsing works with or without `X-Tenant-ID`.
- Authenticated actions require the header (or subdomain-based resolution) and an active membership.
- Frontend should store the tenant context and send `X-Tenant-ID` on every authenticated request.
- If membership is missing or suspended: `403 TENANT_FORBIDDEN`.

## Notification Channels

| Channel | Trigger | User control |
|---------|---------|-------------|
| **In-app** | Order status changes, support replies | `/notifications` endpoints |
| **Email** | Password reset, email verification, shipping updates | Toggle via `NOTIFY_EMAIL_TOPICS` |
| **Web push** | Configurable per user | `/push/preferences` |
| **Webhook** | All domain events to merchant endpoints | Admin webhook management |

## Admin Panel Patterns

Admin endpoints (`/api/v1/admin/*`) require:
1. Valid JWT.
2. Active tenant membership.
3. `admin` role on the user record.

All admin actions are audit-logged (configurable sampling).

### CRUD Resources

| Resource | List | Create | Read | Update | Delete |
|----------|------|--------|------|--------|--------|
| Products | ✓ | ✓ | ✓ | ✓ | ✓ |
| Categories | ✓ | ✓ | – | ✓ | ✓ |
| Brands | ✓ | ✓ | – | ✓ | ✓ |
| Orders | ✓ | – | ✓ | status | – |
| Vouchers | ✓ | ✓ | – | ✓ | ✓ |
| Flash Sales | ✓ | ✓ | ✓ | ✓ | – |
| Webhooks | ✓ | ✓ | – | ✓ | ✓ |

### Analytics

- `GET /analytics/sales` — daily sales with date range.
- `GET /analytics/top-products` — top N by revenue.
- `GET /analytics/overview` / `GET /admin/analytics/overview` — dashboard summary.

## Rate Limiting Behaviour

| Scope | Default | Header |
|-------|---------|--------|
| Global | 1200 req/min | – |
| Per IP | 240 req/min | `X-RateLimit-*` |
| Per user | 120 req/min | `X-RateLimit-*` |
| Login | 10 req/5min per IP | `X-RateLimit-*` |

When rate limited, the response is `429` with `Retry-After` header.

## Pagination

All list endpoints support `?page=N&limit=N`. Default limit: 20. Max limit: 100. Response includes `pagination.total` for the client to compute total pages.

## Timestamps

All timestamps are ISO 8601 / RFC 3339 in UTC: `2024-01-15T10:30:00Z`.

## Frontend Integration Resources

- [API Contracts](contracts/README.md) — full endpoint documentation
- [Quick Start for Frontend](QUICK_START_FRONTEND.md) — Bahasa Indonesia quick guide
- [TypeScript Types](types.ts) — generated TS interfaces for all API shapes
