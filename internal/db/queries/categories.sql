-- name: ListCategories :many
SELECT id, name, slug, parent_id
FROM categories
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY name ASC;

-- name: GetCategoryByID :one
SELECT id, name, slug, parent_id
FROM categories
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
LIMIT 1;

-- name: GetCategoryBySlug :one
SELECT id, name, slug, parent_id
FROM categories
WHERE slug = $1
LIMIT 1;
