# ADR-011: Tenant-scoped staff roles and permissions

**Status:** Accepted
**Date:** 2026-10

## Context

Admin access used to be a single global check: `users.roles` containing
`admin` (`requireRole("admin")`). It was global, so an admin was admin in every
tenant they belonged to, and it had no granularity (catalog editors, packers
and support agents all needed full admin). Meanwhile any member can make
themselves `OWNER` of a new tenant through `POST /onboarding`, so tenant roles
must never reach data outside their tenant. ADR-005 already puts the
authorization boundary at the tenant membership.

## Decision

- `tenant_memberships.role` is the source of truth for tenant authorization.
  Roles: `owner`, `admin`, `manager`, `catalog`, `fulfillment`, `support`,
  `analyst` (staff) and `customer` (DB values stay uppercase). The role to
  permission matrix is static code in `internal/rbac/roles.go`, not a table.
- `rbac.RequireMembership` replaces the old membership middleware and puts the
  caller's role on the context. The `/admin` group adds `rbac.RequireStaff`
  (deny-by-default for customers), and every endpoint is an `rbac.Guarded`
  value created by `rbac.Require(permission, h)` or `rbac.RequirePlatform(h)`.
  A test walks the real router and fails on any unguarded or unlisted route.
- Platform-global resources (webhooks, webhook deliveries, job queue, audit
  logs, cross-tenant `/analytics/*`) stay behind the global `users.roles`
  `admin` check (`rbac.RequirePlatformAdmin`); no tenant role grants them.
- Admin write paths that filtered by id only (order status, shipment creation,
  voucher create/update/preview) and voucher redemption are tenant-scoped.
- Staff management (`/admin/staff`, `/admin/roles`) only assigns roles to
  existing members of the tenant, serializes mutations per tenant with
  `pg_advisory_xact_lock`, re-reads the actor's role inside the transaction,
  and always keeps one ACTIVE owner. Revoking sets the role back to `customer`.
- Migration 000055 widens the role CHECK (`NOT VALID`) and backfills `ADMIN`
  for existing global admins, so their access is unchanged; 000056 validates
  the constraint without blocking readers.
- Audit sampling only drops safe methods; every mutation is recorded.

## Consequences

- **Positive:** Least-privilege staff roles per tenant; owners of self-created
  tenants cannot read, change or delete other tenants' resources or reach
  platform resources.
- **Positive:** A newly added admin route cannot silently inherit access: it is
  blocked for customers and fails the route-guard test until mapped.
- **Negative:** Changing the matrix needs a deploy; there is no per-tenant
  custom role.
- **Negative:** `users.roles` still matters for platform-admin routes and some
  legacy customer counts. Webhook endpoints are tenant-owned (created in, and
  listed, changed or deleted only within, the request's tenant, and delivered
  only that tenant's events), but managing them still needs the platform-admin
  role; tenant-scoping webhook deliveries, audit logs and the queue is follow-up
  work for their owners.
- **Resolved (decision: per-tenant uniqueness):** `vouchers.code`,
  `products.slug`, `categories.slug`, `brands.name`, `brands.slug` and
  `product_variants.sku` used to be unique database-wide, so once self-service
  owners could create them a `409` revealed that a value existed in some other
  tenant and let one tenant squat common names and slugs for everyone.
  Migrations 000057–000070 make each of them unique per `(tenant_id, ...)`:
  `product_variants` gains a `tenant_id` that a trigger always derives from the
  product, each per-tenant index is built `CONCURRENTLY`, and each global
  constraint is dropped only after its replacement is valid, so uniqueness is
  enforced throughout. Every lookup by code or slug (voucher preview, apply,
  checkout, settlement, CSV import, seeder) is tenant-scoped. Rolling the drop
  migrations back fails once two tenants share a value (see
  docs/ops/RUNBOOK.md).
