-- name: ListCartItems :many
-- The image is joined live rather than snapshotted into cart_items alongside
-- title and unit_price: those are frozen because a cart's price must not shift
-- underneath the shopper, whereas the image is presentational and should track
-- the product.
SELECT ci.id, ci.cart_id, ci.product_id, ci.variant_id, ci.campaign_id, ci.title, ci.slug,
       ci.qty, ci.unit_price, ci.subtotal,
       p.thumbnail AS image_url
FROM cart_items ci
LEFT JOIN products p ON p.id = ci.product_id
WHERE ci.cart_id = $1
ORDER BY ci.title ASC, ci.id;

-- name: CreateCartItem :one
INSERT INTO cart_items (cart_id, product_id, variant_id, campaign_id, title, slug, qty, unit_price, subtotal)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal, campaign_id;

-- name: UpdateCartItemQty :one
UPDATE cart_items
SET qty = $2,
    subtotal = $3
WHERE id = $1
RETURNING id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal, campaign_id;

-- name: DeleteCartItem :exec
DELETE FROM cart_items
WHERE id = $1
  AND cart_id = $2;

-- name: FindCartItemByProductVariant :one
SELECT id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal, campaign_id
FROM cart_items
WHERE cart_id = $1
  AND product_id = $2
  AND (variant_id IS NOT DISTINCT FROM $3)
  AND (campaign_id IS NOT DISTINCT FROM $4)
LIMIT 1;

-- name: GetCartItemByID :one
SELECT id, cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal, campaign_id
FROM cart_items
WHERE id = $1
LIMIT 1;
