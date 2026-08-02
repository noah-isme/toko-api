-- name: ListBrands :many
SELECT id, name, slug
FROM brands
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY name ASC;

-- name: GetBrandByID :one
SELECT id, name, slug
FROM brands
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
LIMIT 1;

-- name: GetBrandBySlug :one
SELECT id, name, slug
FROM brands
WHERE slug = $1
LIMIT 1;
