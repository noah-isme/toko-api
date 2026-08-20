# ADR-004: JWT Authentication with Refresh Tokens

**Status:** Accepted  
**Date:** 2024-02  

## Context

The API serves a web SPA and potentially mobile clients. We need stateless authentication (no server-side session lookup on every request) while still supporting token revocation and rotation.

## Decision

Use **short-lived JWT access tokens** (configurable, default 15 min) + **long-lived refresh tokens** stored in PostgreSQL `sessions` table and delivered via an `HttpOnly`, `Secure`, `SameSite=Lax` cookie.

- Access tokens are verified in-process using `lestrrat-go/jwx` (supports HMAC and RSA).
- Refresh endpoint rotates the token and session row atomically.
- Logout deletes the session; logout-all deletes all sessions for the user.
- Password hashing uses Argon2id via `alexedwards/argon2id`.

## Consequences

- **Positive:** Request authentication is stateless — no DB hit on every request.
- **Positive:** Refresh tokens in `HttpOnly` cookies are resistant to XSS.
- **Positive:** Session table enables "list active sessions" and "revoke all".
- **Negative:** Access tokens remain valid until expiry even after password change (mitigated by short TTL).
- **Negative:** Clock skew between servers must be within `JWT_CLOCK_SKEW` (default 30s).
