-- Staff RBAC queries. Every statement is scoped by tenant_id; this file is
-- scanned by cmd/tools/tenant_guard (admin_ prefix).

-- name: GetTenantMembershipRole :one
SELECT role
FROM tenant_memberships
WHERE tenant_id = sqlc.arg(tenant_id)
  AND user_id = sqlc.arg(user_id)
  AND status = 'ACTIVE';

-- name: GetTenantMembershipForUpdate :one
SELECT tenant_id, user_id, role, status, created_at, updated_at
FROM tenant_memberships
WHERE tenant_id = sqlc.arg(tenant_id)
  AND user_id = sqlc.arg(user_id)
FOR UPDATE;

-- name: GetTenantMemberUserIDByEmail :one
-- Resolves an email to a user only when that user already has a membership in
-- the tenant, so staff assignment cannot probe or enrol accounts platform-wide.
SELECT tm.user_id
FROM tenant_memberships tm
JOIN users u ON u.id = tm.user_id
WHERE tm.tenant_id = sqlc.arg(tenant_id)
  AND u.email = sqlc.arg(email)
LIMIT 1;

-- name: CountTenantOwners :one
SELECT COUNT(*)
FROM tenant_memberships
WHERE tenant_id = sqlc.arg(tenant_id)
  AND role = 'OWNER'
  AND status = 'ACTIVE';

-- name: ListTenantStaff :many
SELECT tm.user_id, u.name, u.email, tm.role, tm.status, tm.created_at, tm.updated_at
FROM tenant_memberships tm
JOIN users u ON u.id = tm.user_id
WHERE tm.tenant_id = sqlc.arg(tenant_id)
  AND tm.role <> 'CUSTOMER'
ORDER BY CASE tm.role WHEN 'OWNER' THEN 0 WHEN 'ADMIN' THEN 1 WHEN 'MANAGER' THEN 2 ELSE 3 END,
         u.email
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountTenantStaff :one
SELECT COUNT(*)
FROM tenant_memberships tm
WHERE tm.tenant_id = sqlc.arg(tenant_id)
  AND tm.role <> 'CUSTOMER';

-- name: GetTenantStaffMember :one
SELECT tm.user_id, u.name, u.email, tm.role, tm.status, tm.created_at, tm.updated_at
FROM tenant_memberships tm
JOIN users u ON u.id = tm.user_id
WHERE tm.tenant_id = sqlc.arg(tenant_id)
  AND tm.user_id = sqlc.arg(user_id);

-- name: UpdateTenantMembershipRole :one
UPDATE tenant_memberships
SET role = sqlc.arg(role),
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND user_id = sqlc.arg(user_id)
RETURNING tenant_id, user_id, role, status, created_at, updated_at;

-- name: ActivateTenantMembershipRole :one
-- Used by staff assignment: grants the role and reactivates a SUSPENDED membership.
UPDATE tenant_memberships
SET role = sqlc.arg(role),
    status = 'ACTIVE',
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)
  AND user_id = sqlc.arg(user_id)
RETURNING tenant_id, user_id, role, status, created_at, updated_at;

-- name: LockTenantStaff :exec
-- Transaction-scoped advisory lock serializing staff mutations per tenant.
SELECT pg_advisory_xact_lock(hashtextextended('tenant_staff:' || sqlc.arg(tenant_id)::uuid::text, 0));
