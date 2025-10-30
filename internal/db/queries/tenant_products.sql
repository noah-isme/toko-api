-- name: ListProductsByTenant :many
SELECT id,
       slug,
       title,
       price,
       in_stock
FROM products
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: GetProductDetailByTenant :one
SELECT id,
       slug,
       title,
       price,
       in_stock
FROM products
WHERE tenant_id = sqlc.arg(tenant_id)
  AND slug = sqlc.arg(slug)
LIMIT 1;
