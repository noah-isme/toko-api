-- name: ListCartItems :many
SELECT id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal
FROM cart_items
WHERE cart_id = $1
ORDER BY title ASC, id;

-- name: CreateCartItem :one
INSERT INTO cart_items (cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal;

-- name: UpdateCartItemQty :one
UPDATE cart_items
SET qty = $2,
    subtotal = $3
WHERE id = $1
RETURNING id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal;

-- name: DeleteCartItem :exec
DELETE FROM cart_items
WHERE id = $1
  AND cart_id = $2;

-- name: FindCartItemByProductVariant :one
SELECT id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal
FROM cart_items
WHERE cart_id = $1
  AND product_id = $2
  AND (variant_id IS NOT DISTINCT FROM $3)
LIMIT 1;

-- name: GetCartItemByID :one
SELECT id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal
FROM cart_items
WHERE id = $1
LIMIT 1;
