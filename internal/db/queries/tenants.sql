-- name: GetTenantTimezone :one
SELECT timezone FROM tenants WHERE id = $1;
