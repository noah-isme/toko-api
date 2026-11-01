-- name: GetTenantTimezone :one
SELECT timezone FROM tenants WHERE id = $1;

-- Tenants the periodic worker jobs iterate over.
-- name: ListActiveTenantIDs :many
SELECT id
FROM tenants
WHERE status = 'active'
ORDER BY created_at, id;
