# Design Guide — Toko API

This document describes how the API should behave from the frontend's perspective: response shapes, error conventions, authentication flows, and UX-critical interaction patterns.

## API Conventions

### Base URL & Versioning

Application endpoints are mounted below `/api/v1`. The operational endpoints are
root-level exceptions:

- `GET /health/live` returns the liveness probe.
- `GET /health/ready` returns dependency readiness.
- `GET /metrics` is exposed when Prometheus metrics are enabled.
- `/debug/pprof/*` is exposed only when pprof is enabled (and can be protected
  with HTTP Basic authentication).

Collection roots are registered with a trailing slash for `/api/v1/carts/`,
`/api/v1/favorites/`, and `/api/v1/users/me/addresses/`; address item routes
also end in `/` (for example, `/api/v1/users/me/addresses/{addressID}/`). Other
routes use the non-slash form shown below. The router does not provide a
universal trailing-slash policy, so clients should use the exact route
documented by each contract.

### Response Envelope

There is no single envelope for every successful response. Most application
handlers wrap their result in a `data` property:

```json
{
  "data": {
    "id": "uuid",
    "name": "Product Name",
    "createdAt": "2024-01-15T10:30:00Z"
  }
}
```

Paginated handlers return `data` (an array) and `pagination`. The common
pagination field names are `page`, `perPage`, and `totalItems`:

```json
{
  "data": [...],
  "pagination": {
    "page": 1,
    "perPage": 20,
    "totalItems": 142
  }
}
```

This convention is route-specific. Some handlers return a raw JSON value
(including reviews, product questions, favorites, and loyalty endpoints), the
refresh endpoint returns a raw `{"accessToken":"..."}` object, and `204`
responses have no body. Check the endpoint contract before decoding a response.

### Error Shape

Application handlers normally use this JSON error shape:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "Human-readable description",
    "details": { ... }
  }
}
```

Codes are route-specific; common examples include `BAD_REQUEST`,
`VALIDATION_ERROR`, `NOT_FOUND`, `UNAUTHORIZED`, `FORBIDDEN`,
`TENANT_FORBIDDEN`, `CONFLICT`, and `INTERNAL`. Middleware-level failures are
not all JSON-enveloped: CSRF, body-size, CORS, and rate-limit rejections use
plain text, while readiness returns a small `{ "db": ..., "redis": ... }`
JSON object. The idempotency replay response is a JSON error with its own
`IDEMPOTENT_REPLAY` code.

### HTTP Status Codes

| Status | Meaning |
|--------|---------|
| `200` | Successful request (common for GET/PATCH and some POST endpoints) |
| `201` | Created (selected POST endpoints, including registration, cart creation, and checkout) |
| `204` | No response body (selected logout, reset, forgot-password, delete, and status-transition endpoints) |
| `400` | Invalid input or validation failure |
| `401` | Missing/invalid bearer token or missing/invalid refresh cookie |
| `403` | CSRF failure, missing tenant membership, or insufficient role |
| `404` | Resource not found |
| `409` | Resource conflict or idempotency replay |
| `413` | Request body exceeds the configured limit |
| `429` | Rate limit exceeded (plain-text response) |
| `502` | Upstream shipping-provider failure |
| `503` | Readiness or selected provider is unavailable |
| `500` | Internal server error |

## Authentication Flow

```
┌─────────┐                         ┌─────────┐
│  Client  │                         │   API   │
└────┬────┘                         └────┬────┘
     │ POST /api/v1/auth/register         │
     │───────────────────────────────────▶│
     │◀──────── 201 { data: user }────────│
     │                                    │
     │ POST /api/v1/auth/login             │
     │───────────────────────────────────▶│
     │◀────── 200 { data: { user,          │
     │       accessToken, ... } }─────────│
     │          Set-Cookie: rt (default)  │
     │                                    │
     │ GET /api/v1/products               │
     │ Authorization: Bearer <access>     │
     │───────────────────────────────────▶│
     │◀──────── 200 { data }──────────────│
     │                                    │
     │ POST /api/v1/auth/refresh           │
     │ Cookie: rt (default)               │
     │───────────────────────────────────▶│
     │◀────── 200 { accessToken }─────────│
     │          Set-Cookie: rt (rotated)  │
     │                                    │
     │ POST /api/v1/auth/logout            │
     │ Cookie: rt (optional)               │
     │───────────────────────────────────▶│
     │◀──── 204; refresh cookie cleared ──│
```

- **Registration** returns `201` with `{"data": user}`. It does not issue an
  access or refresh token.
- **Access token**: a short-lived JWT sent in the `Authorization: Bearer`
  header. The default lifetime is 15 minutes (`ACCESS_TOKEN_TTL`). Protected
  routes require this header; the API's configured auth middleware does not
  read an access-token cookie.
- **Refresh token**: never returned in the JSON body. Login and refresh set an
  `HttpOnly` cookie whose name defaults to `rt` and is configurable with
  `REFRESH_COOKIE_NAME`; its path defaults to `/api/v1/auth`. `Secure` and
  `SameSite` are controlled by `REFRESH_COOKIE_SECURE` and
  `REFRESH_COOKIE_SAMESITE` (default `Lax`), and the token is rotated on every
  refresh. The default refresh lifetime is 720 hours (`REFRESH_TOKEN_TTL`).
- `/api/v1/auth/refresh` authenticates with that refresh cookie only; it returns
  a raw `{"accessToken":"..."}` object and rotates the cookie.
- `/api/v1/auth/logout` is not bearer-protected. It revokes the refresh cookie
  when present, clears it, and returns `204`. `/api/v1/auth/me`,
  `/api/v1/auth/sessions`, and `/api/v1/auth/logout/all` require a valid bearer
  token and active tenant membership.
- `/api/v1/auth/password/forgot` and `/api/v1/auth/password/reset` return
  `204` without a response body. `/api/v1/auth/email/verify` and
  `/api/v1/auth/email/resend` return `200` with a `data` object; the resend
  endpoint intentionally does not reveal whether an address is registered.

### CSRF

When `SECURITY_CSRF_ENABLED` is true (the API default), the CSRF middleware is
applied globally. It skips `GET`, `HEAD`, `OPTIONS`, and `TRACE`, and it also
skips any request whose `Authorization` value starts with `Bearer ` (case
insensitive). Every other method must provide both:

1. the configured header (`SECURITY_CSRF_HEADER`, default `X-CSRF-Token`); and
2. a cookie with the same name and exactly the same value.

Safe requests that reach this middleware (`GET`, `HEAD`, `OPTIONS`, and `TRACE`)
bootstrap a cryptographically random token when the same-name cookie is absent.
The cookie is intentionally readable by browser JavaScript (`HttpOnly=false`)
so a client can copy its value into the configured header. It defaults to
`Path=/`, `SameSite=Lax`, and a 24-hour lifetime; `Secure`, `SameSite`, `Domain`,
`Path`, and lifetime are configurable with `SECURITY_CSRF_COOKIE_SECURE`,
`SECURITY_CSRF_COOKIE_SAMESITE`, `SECURITY_CSRF_COOKIE_DOMAIN`,
`SECURITY_CSRF_COOKIE_PATH`, and `SECURITY_CSRF_COOKIE_MAX_AGE_SEC`. The
`Secure` default is enabled outside development, and should remain enabled for
HTTPS staging/production deployments.

Inbound provider webhooks under `/api/v1/webhooks/*` are exempt because they
authenticate through provider signatures/tokens rather than browser cookies.
CSRF rejections are `403` plain-text responses. A bearer request still needs a
valid token wherever the route requires authentication; the bearer exemption
only skips this CSRF check.

## Cart & Checkout UX

### Routes and Authentication

```
1. Shopper creates a guest cart → `POST /api/v1/carts/` (the collection route
   includes the trailing slash).
2. Shopper adds or changes items → `POST /api/v1/carts/{id}/items`,
   `PATCH /api/v1/carts/{id}/items/{itemId}`, or
   `DELETE /api/v1/carts/{id}/items/{itemId}`.
3. Shopper applies/removes a voucher → `POST /api/v1/carts/{id}/apply-voucher`
   or `DELETE /api/v1/carts/{id}/voucher`.
4. Shopper requests quotes → `POST /api/v1/carts/{id}/quote/shipping` or
   `POST /api/v1/carts/{id}/quote/tax`.
5. Shopper logs in and merges the guest cart → `POST /api/v1/carts/merge`.
   This route requires a bearer token and active tenant membership.
6. Shopper previews checkout → `POST /api/v1/checkout/draft`. This route
   requires a bearer token and active tenant membership, returns `201` with
   `data`, and persists no order.
7. Shopper confirms → `POST /api/v1/checkout`. This route has the same auth and
   tenant requirements and returns `201` with `data` containing the order
   result.
```

- Cart creation and cart item/voucher/quote writes are guest-capable routes;
  they are not wrapped in `RequireAuth`. `GET /api/v1/carts/` resolves an
  anonymous cart from the `anonId` query parameter when no user context is
  already present. Tenant and ownership checks are route-specific; handlers
  use the resolved tenant for the operations whose database queries accept a
  tenant scope.
- Checkout commits the order, order items, and inventory reservations in a
  database transaction. Payment-intent creation happens after that commit;
  if it fails, the service attempts to release reservations and cancel the
  order. The checkout operation is therefore not one transaction spanning the
  payment provider.

### Idempotency

The Redis-backed `Idem` middleware wraps all mutating routes in the `/carts`
group, both checkout routes, and `POST /api/v1/payments/intent`. Clients may
send an `Idempotency-Key` header. With no header, or when Redis is not
configured, the request proceeds without an idempotency check.

The first request stores a lock for the configured TTL (default 600 seconds,
`IDEMPOTENCY_TTL_SEC`). A later request with the same key receives `409` and
this JSON body; the original response is not saved or replayed:

```json
{
  "error": {
    "code": "IDEMPOTENT_REPLAY",
    "message": "duplicate request"
  }
}
```

The Redis key is derived from the header alone rather than the route or user,
so clients should use a unique key for each logical operation across these
endpoints. Redis errors return an `INTERNAL` JSON error; they do not silently
create a replay response.

## Multi-Tenant UX

- Every `/api/v1` request passes through tenant resolution. The resolver checks
  `X-Tenant-ID`, then a subdomain of the configured root domain, then the
  default tenant resolved at startup (`TENANT_DEFAULT_ID` or the database row
  with slug `default`).
- Public catalog browsing works without authentication; it uses the resolved
  tenant, including the default when no header or matching subdomain is sent.
- Protected user, checkout, payment, analytics, and admin routes require a
  valid bearer token and an `ACTIVE` membership in the resolved tenant. The
  guest cart and public auth routes are deliberate exceptions; cart merge and
  checkout are protected.
- Frontend should store the tenant context and send `X-Tenant-ID` on every
  authenticated request when it is not using a tenant subdomain.
- If membership is missing or suspended: `403 TENANT_FORBIDDEN`.

## Notification Channels

| Channel | Trigger | User control |
|---------|---------|-------------|
| **In-app** | Order status changes, support replies | `/api/v1/notifications` endpoints |
| **Email** | Password reset, email verification, shipping updates | `NOTIFY_EMAIL_ENABLED` and `NOTIFY_EMAIL_TOPIC_*` settings |
| **Web push** | Configurable per user | `/api/v1/push/preferences` |
| **Webhook** | Configured domain events delivered to merchant endpoints | `/api/v1/admin/webhooks` management |

## Admin Panel Patterns

Admin endpoints (`/api/v1/admin/*`) require, in this order:
1. Valid JWT.
2. Active tenant membership.
3. The exact lowercase `admin` role in the user record's roles.

The admin and `/api/v1/auth/*` route groups are passed through the HTTP audit
recorder. Persistence is controlled by `AUDIT_ENABLED` and
`AUDIT_SAMPLING_RATE` (default enabled with a sampling rate of `1.0`), so a
disabled or sampled-out request is not guaranteed to have a stored audit row.

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

- `GET /api/v1/analytics/sales` — daily sales with date range (admin only).
- `GET /api/v1/analytics/top-products` — top N by revenue (admin only).
- `GET /api/v1/analytics/overview` and
  `GET /api/v1/admin/analytics/overview` — dashboard summaries (admin only).

## Rate Limiting Behaviour

The `/api/v1` router applies three Redis-backed sliding-window limiters to
every request. Defaults are configurable with the corresponding environment
variables:

| Scope | Default window and maximum | Redis key |
|-------|----------------------------|-----------|
| Global | 60 seconds / 1200 requests | `global` |
| Per IP | 60 seconds / 240 requests | `ip:<client-ip>` |
| User or anonymous IP | 60 seconds / 120 requests | `user:<id>` or `anon:<client-ip>` |

`POST /api/v1/auth/login`, `/api/v1/auth/password/forgot`,
`/api/v1/auth/password/reset`, and `/api/v1/auth/email/resend` also use a
per-IP limiter of 10 requests per 300 seconds. The global, IP, user, and login
limits use `RATE_LIMIT_*_WINDOW_SEC` and `RATE_LIMIT_*_MAX` settings.

The user limiter runs before route-level `RequireAuth` in the current router,
so protected requests normally reach it using the anonymous-IP key rather than
the authenticated-user key.

Client-IP identity is the direct network peer (`RemoteAddr`). `X-Forwarded-For`
and `X-Real-IP` are ignored, and the API does not enable chi's unconditional
`RealIP` middleware. This prevents callers from spoofing rate-limit, login,
or audit identities. If a staging or production reverse proxy is introduced,
it must provide an explicitly trusted client-IP mechanism before forwarded
headers can be used; do not treat these headers as trusted configuration.

Allowed responses include `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and
`X-RateLimit-Reset`. When a request is rejected, the response is `429` plain
text with a `Retry-After` header. If Redis is unavailable or a limiter call
errors, the middleware logs the error and fails open by allowing the request.


## Pagination

Standard paginated catalog, order, notification, admin, and admin flash-sale
list endpoints accept `?page=N&limit=N` and return `pagination.page`,
`pagination.perPage`, and `pagination.totalItems`. Their usual default is page
`1`, 20 items, with a maximum of 100 (individual endpoints may use different
bounds; for example, audit logs default to 50 and allow up to 200).
Non-paginated list handlers do not add pagination metadata, so clients must
follow each endpoint contract.

## Timestamps

All timestamps are ISO 8601 / RFC 3339 in UTC: `2024-01-15T10:30:00Z`.

## Frontend Integration Resources

- [API Contracts](contracts/README.md) — full endpoint documentation
- [Quick Start for Frontend](QUICK_START_FRONTEND.md) — Bahasa Indonesia quick guide
- [TypeScript Types](types.ts) — generated TS interfaces for all API shapes
