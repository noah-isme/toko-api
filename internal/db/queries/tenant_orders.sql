-- name: ListOrdersByTenant :many
SELECT id,
       user_id,
       status,
       created_at
FROM orders
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(status) IS NULL OR status = sqlc.arg(status))
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: GetOrderByTenant :one
SELECT id,
       user_id,
       status,
       created_at
FROM orders
WHERE tenant_id = sqlc.arg(tenant_id)
  AND id = sqlc.arg(id)
LIMIT 1;
