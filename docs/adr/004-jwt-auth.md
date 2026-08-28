# ADR-004: JWT Authentication with Refresh Tokens

**Status:** Accepted
**Date:** 2024-02

## Context

The API serves a web SPA and potentially mobile clients. We need stateless authentication (no server-side session lookup on every request) while still supporting token revocation and rotation.

## Decision

Use **short-lived HS256 JWT access tokens** (configurable, default 15 min) plus **long-lived refresh tokens** stored as SHA-256 hashes in the PostgreSQL `sessions` table. The raw refresh token is delivered in an `HttpOnly` cookie; `Secure` is required for production and `SameSite=Lax` is the default.

- Access tokens are signed and verified in-process using `lestrrat-go/jwx` with HS256.
- The refresh endpoint validates the session, then rotates its token and expiry with one database `UPDATE`.
- Logout deletes the session; logout-all deletes all sessions for the user.
- Password hashing uses Argon2id via `alexedwards/argon2id`.

## Consequences

- **Positive:** Request authentication is stateless — no DB hit on every request.
- **Positive:** Refresh tokens in `HttpOnly` cookies are resistant to XSS.
- **Positive:** Session table enables "list active sessions" and "revoke all".
- **Negative:** Access tokens remain valid until expiry even after password change (mitigated by short TTL).
- **Negative:** Clock skew between servers is accepted according to `JWT_CLOCK_SKEW_SEC` (default 60 seconds); deployments should still synchronize their clocks.
