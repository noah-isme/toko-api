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
  legacy customer counts; tenant-scoping webhooks, audit logs and analytics is
  follow-up work for their owners.
- **Negative (open, needs a decision before merge):** `vouchers.code`,
  `products.slug`, `categories.slug`, `brands.name`, `brands.slug` and
  `product_variants.sku` are still unique database-wide. Before this change only
  global admins could create them; now any self-service owner can. A `409` on
  create reveals that a value exists in some other tenant, but not which one
  (for a voucher code, an attacker who wants to redeem it would still have to
  find the owning tenant by joining tenants as a customer and trying the code
  in each), and a tenant can squat common names and slugs for everyone. Redemption itself is
  tenant-scoped, so a foreign code never discounts another tenant's cart. The
  fix is per-tenant uniqueness: one `CREATE UNIQUE INDEX CONCURRENTLY` migration
  per `(tenant_id, ...)` index, then dropping the global constraints
  (`product_variants` has no `tenant_id` and needs one first), plus updating
  every `ON CONFLICT (slug|code|sku)` in the seeder and backfill tool and the
  lookups by code or slug. That needs more migration numbers than this stream
  was assigned and touches catalog, search and tooling code, so it is either
  done as a follow-up before merge or explicitly deferred by the product owner.
