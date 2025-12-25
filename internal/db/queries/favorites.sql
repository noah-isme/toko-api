-- name: AddFavorite :exec
INSERT INTO favorites (user_id, product_id, tenant_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, product_id) DO NOTHING;

-- name: RemoveFavorite :exec
DELETE FROM favorites
WHERE user_id = $1 AND product_id = $2 AND tenant_id = $3;

-- name: ListFavorites :many
SELECT f.product_id, f.created_at, p.title as product_name, p.slug as product_slug, p.price, p.thumbnail as image_url
FROM favorites f
JOIN products p ON f.product_id = p.id
WHERE f.user_id = $1 AND f.tenant_id = $2
ORDER BY f.created_at DESC;

-- name: CheckFavorite :one
SELECT 1 
FROM favorites 
WHERE user_id = $1 AND product_id = $2 AND tenant_id = $3
LIMIT 1;
