# ADR-005: Header-Based Multi-Tenancy with Membership Guard

**Status:** Accepted
**Date:** 2024-06

## Context

Toko needs to support multiple independent storefronts (tenants) on a shared infrastructure. Each tenant has its own catalog seed, vouchers, and orders, but shares the same database and API deployment.

## Decision

Implement **shared-database, row-scoped** multi-tenancy:

- Tenant resolution via `X-Tenant-ID` header or subdomain matching (handled by `tenant.Resolver` middleware).
- A `tenants` table stores tenant metadata; a `tenant_memberships` table links users to tenants with a status (`ACTIVE`, `SUSPENDED`).
- Protected tenant workflows pass through `requireTenantMembership` middleware, which validates that the authenticated user has an `ACTIVE` row for the resolved tenant. Public catalog reads and inbound webhooks are intentionally outside that membership check.
- Public read endpoints (catalog browse) work without membership.
- A default tenant (slug `'default'`) is seeded by migration `000018` and resolved at startup.

## Consequences

- **Positive:** Single deployment serves all tenants — no per-tenant infrastructure.
- **Positive:** Tenant isolation at the application level is simpler to operate than schema-per-tenant.
- **Negative:** Tenant-aware queries must scope by `tenant_id` or by a tenant-scoped parent key; a missing scope can leak data across tenants. The `tenant_guard` tool (`cmd/tools/tenant_guard`) runs on pull requests and checks statement-level filters for required tables in `admin_*.sql`, but it remains a heuristic: it does not cover non-admin SQL, CTEs, inserts, or application-level isolation.
- **Negative:** Noisy-neighbour risk on shared DB — mitigated by connection pool limits and rate limiting.
