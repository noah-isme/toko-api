-- Tenants the periodic worker jobs iterate over.
-- name: ListActiveTenantIDs :many
SELECT id
FROM tenants
WHERE status = 'active'
ORDER BY created_at, id;
