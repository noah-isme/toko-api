# ADR-005: Header-Based Multi-Tenancy with Membership Guard

**Status:** Accepted  
**Date:** 2024-06  

## Context

Toko needs to support multiple independent storefronts (tenants) on a shared infrastructure. Each tenant has its own catalog seed, vouchers, and orders, but shares the same database and API deployment.

## Decision

Implement **shared-database, schema-per-row** multi-tenancy:

- Tenant resolution via `X-Tenant-ID` header or subdomain matching (handled by `tenant.Resolver` middleware).
- A `tenants` table stores tenant metadata; a `tenant_memberships` table links users to tenants with a status (`ACTIVE`, `SUSPENDED`).
- Every authenticated, write-path request passes through `requireTenantMembership` middleware that validates the user has an `ACTIVE` row for the resolved tenant.
- Public read endpoints (catalog browse) work without membership.
- A default tenant (slug `'default'`) is seeded by migration `000018` and resolved at startup.

## Consequences

- **Positive:** Single deployment serves all tenants — no per-tenant infrastructure.
- **Positive:** Tenant isolation at the application level is simpler to operate than schema-per-tenant.
- **Negative:** Every tenant query must include a `tenant_id` filter; missing filters leak data across tenants. Mitigated by the `tenant_guard` tool (`cmd/tools/tenant_guard`) that audits queries.
- **Negative:** Noisy-neighbour risk on shared DB — mitigated by connection pool limits and rate limiting.
