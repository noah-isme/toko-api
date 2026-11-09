# Authentication & User Endpoints

> **Last Updated:** 2026-10-25

## 1.1 Register

```http
POST /api/v1/auth/register
Content-Type: application/json
```

**Request:**
```json
{
  "name": "John Doe",
  "email": "john@example.com",
  "password": "SecurePass123!"
}
```

**Response:** `201 Created`
```json
{
  "data": {
    "user": {
      "id": "uuid-here",
      "name": "John Doe",
      "email": "john@example.com",
      "createdAt": "2025-12-07T10:00:00Z"
    },
    "accessToken": "eyJhbGc..."
  }
}
```

**Set-Cookie:** `refresh_token=...; HttpOnly; Secure; SameSite=Lax; Path=/api/v1/auth`

---

## 1.2 Login

```http
POST /api/v1/auth/login
Content-Type: application/json
```

**Request:**
```json
{
  "email": "john@example.com",
  "password": "SecurePass123!"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "user": {
      "id": "uuid-here",
      "name": "John Doe",
      "email": "john@example.com"
    },
    "accessToken": "eyJhbGc..."
  }
}
```

**Set-Cookie:** `refresh_token=...`

---

## 1.3 Refresh Token

```http
POST /api/v1/auth/refresh
Cookie: refresh_token=...
```

**Response:** `200 OK`
```json
{
  "data": {
    "accessToken": "eyJhbGc..."
  }
}
```

---

## 1.4 Logout

```http
POST /api/v1/auth/logout
Authorization: Bearer <token>
Cookie: refresh_token=...
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Logged out successfully"
  }
}
```

**Set-Cookie:** `refresh_token=; Max-Age=0` (clears cookie)

---

## 1.5 Get Current User

```http
GET /api/v1/auth/me
Authorization: Bearer <token>
```

Requires `X-Tenant-ID` (or a tenant subdomain) and an ACTIVE membership in
that tenant (`403 TENANT_FORBIDDEN` otherwise).

**Response:** `200 OK`
```json
{
  "data": {
    "id": "uuid-here",
    "name": "John Doe",
    "email": "john@example.com",
    "phone": "+6281234567890",
    "roles": ["customer"],
    "role": "manager",
    "permissions": ["analytics:read", "catalog:read", "catalog:write", "..."],
    "emailVerified": false,
    "createdAt": "2025-12-07T10:00:00Z",
    "updatedAt": "2025-12-07T10:00:00Z"
  }
}
```

- `role` — the caller's role in the resolved tenant: one of `owner`, `admin`,
  `manager`, `catalog`, `fulfillment`, `support`, `analyst`, or `customer` for
  non-staff. Present on this endpoint only (login/register responses omit it).
- `permissions` — the sorted permissions of `role` (`[]` for `customer`). Gate
  every tenant-scoped admin UI on this list, not on `role` (see 1.12).
- `roles` — legacy global roles (`users.roles`), kept for compatibility. Its
  only remaining gating use: the platform-admin areas (webhooks, webhook
  deliveries, job queue, audit logs, `/analytics/*`) are available iff `roles`
  contains `"admin"`.

---

## 1.6 Forgot Password

```http
POST /api/v1/auth/password/forgot
Content-Type: application/json
```

**Request:**
```json
{
  "email": "john@example.com"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Password reset email sent"
  }
}
```

---

## 1.7 Reset Password

```http
POST /api/v1/auth/password/reset
Content-Type: application/json
```

**Request:**
```json
{
  "token": "reset-token-from-email",
  "newPassword": "NewSecurePass123!"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "Password reset successfully"
  }
}
```

---

## 1.8 Verify Email

```http
POST /api/v1/auth/email/verify
Content-Type: application/json
```

**Request:**
```json
{
  "token": "verification-token-from-email"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "email verified",
    "user": {
      "id": "uuid-here",
      "name": "John Doe",
      "email": "john@example.com",
      "emailVerified": true,
      "createdAt": "2025-12-07T10:00:00Z",
      "updatedAt": "2025-12-08T09:00:00Z"
    }
  }
}
```

**Errors:** `400 INVALID_TOKEN` — token unknown, already used, or expired. Tokens
are single-use and valid for 72 hours.

---

## 1.9 Resend Verification Email

```http
POST /api/v1/auth/email/resend
Content-Type: application/json
```

**Request:**
```json
{
  "email": "john@example.com"
}
```

**Response:** `200 OK`
```json
{
  "data": {
    "message": "verification email sent"
  }
}
```

Always returns `200` — unknown and already-verified addresses are indistinguishable
from successful sends so the endpoint cannot be used to enumerate accounts.

---

## 1.10 List Sessions

```http
GET /api/v1/auth/sessions
Authorization: Bearer <token>
```

**Response:** `200 OK`
```json
{
  "data": [
    {
      "id": "session-uuid",
      "device": "Chrome on macOS",
      "ipAddress": "203.0.113.9",
      "lastActive": "2026-10-25T08:00:00Z",
      "current": false
    }
  ]
}
```

One entry per stored refresh session, newest first. Expired sessions are only
purged when they are next used, so they can still appear. `lastActive` is the
session's creation time, `current` is always `false` today (the current session
is not tracked) and `location` is not populated yet (omitted). **Errors:**
`401 UNAUTHORIZED`, `403 TENANT_FORBIDDEN`.

---

## 1.11 Logout Everywhere

```http
POST /api/v1/auth/logout/all
Authorization: Bearer <token>
```

**Response:** `204 No Content`. Revokes every refresh session of the caller and
clears the refresh cookie. Access tokens already issued stay valid until they
expire. **Errors:** `401 UNAUTHORIZED`, `403 TENANT_FORBIDDEN`.

---

## 1.12 Roles & Permissions

Authorization for admin endpoints comes from the caller's membership role in
the resolved tenant (`tenant_memberships.role`), not from the global
`users.roles`. A role change takes effect on the caller's next request.

**Permissions:** `catalog:read`, `catalog:write`, `inventory:read`,
`inventory:write`, `orders:read`, `orders:write`, `returns:read`,
`returns:write`, `refunds:write`, `customers:read`, `support:read`,
`support:write`, `promotions:read`, `promotions:write`, `analytics:read`,
`settings:read`, `settings:write`, `staff:read`, `staff:manage`.

| Role | Permissions |
|---|---|
| `owner` | all |
| `admin` | all (cannot grant `owner` or change/revoke an owner) |
| `manager` | all except `settings:write`, `staff:manage` |
| `catalog` | `catalog:read`, `catalog:write`, `inventory:read`, `inventory:write`, `promotions:read` |
| `fulfillment` | `orders:read`, `orders:write`, `returns:read`, `returns:write`, `inventory:read`, `customers:read` |
| `support` | `support:read`, `support:write`, `orders:read`, `returns:read`, `returns:write`, `customers:read` |
| `analyst` | `analytics:read`, `orders:read`, `catalog:read`, `promotions:read`, `customers:read` |
| `customer` | none (non-staff; cannot reach `/admin`) |

`GET /api/v1/admin/roles` returns this matrix from the server; render role
pickers from it.

**Platform admin.** Webhooks, webhook deliveries, the job queue, audit logs and
`/api/v1/analytics/*` are platform-global (not tenant-scoped) and are outside
the matrix: they require the global `users.roles` to contain `admin`, exactly
as before, so no tenant role (not even `owner` of a self-created tenant)
grants them. Existing global admins also became `admin` in every tenant they
belong to, so their access is unchanged. `/api/v1/analytics/*` moves to
`analytics:read` once it is tenant-scoped.

**Error precedence on `/api/v1/admin/*`:**

1. `401 UNAUTHORIZED` — no or invalid token.
2. `403 TENANT_FORBIDDEN` — no ACTIVE membership in the tenant.
3. `403 FORBIDDEN`, `details.required = "staff"` — member whose role is `customer`.
4. `403 FORBIDDEN`, `details.required = "platform-admin"` — platform-admin routes only.
5. `403 FORBIDDEN`, `details.required = "<permission>"` — role lacks the route's permission.
6. The handler. Reading, changing or deleting a resource of another tenant
   by id or code returns `404`, never `403`. Voucher codes, product/category
   slugs, brand names/slugs and variant SKUs are unique per tenant, so another
   tenant's values never cause a `409 CONFLICT` (see ADR-011, Consequences).

```json
{ "error": { "code": "FORBIDDEN", "message": "insufficient permissions", "details": { "required": "orders:write" } } }
```
