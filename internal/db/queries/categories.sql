-- name: ListCategories :many
SELECT id, name, slug, parent_id
FROM categories
ORDER BY name ASC;

-- name: GetCategoryByID :one
SELECT id, name, slug, parent_id
FROM categories
WHERE id = $1
LIMIT 1;

-- name: GetCategoryBySlug :one
SELECT id, name, slug, parent_id
FROM categories
WHERE slug = $1
LIMIT 1;
