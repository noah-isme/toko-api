-- name: ListBrands :many
SELECT id, name, slug
FROM brands
ORDER BY name ASC;

-- name: GetBrandByID :one
SELECT id, name, slug
FROM brands
WHERE id = $1
LIMIT 1;

-- name: GetBrandBySlug :one
SELECT id, name, slug
FROM brands
WHERE slug = $1
LIMIT 1;
