-- name: CreateOrder :one
INSERT INTO orders (user_id, cart_id, status, currency, pricing_subtotal, pricing_discount, pricing_tax, pricing_shipping, pricing_total, shipping_address, shipping_option, notes, applied_voucher_code)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: CreateOrderItem :exec
INSERT INTO order_items (order_id, product_id, variant_id, title, slug, qty, unit_price, subtotal)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetOrderByIDForUser :one
SELECT *
FROM orders
WHERE id = $1 AND user_id = $2
LIMIT 1;

-- name: GetOrderByID :one
SELECT *
FROM orders
WHERE id = $1
LIMIT 1;

-- name: ListOrdersForUser :many
SELECT *
FROM orders
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountOrdersForUser :one
SELECT COUNT(*)
FROM orders
WHERE user_id = $1;

-- name: UpdateOrderStatus :exec
UPDATE orders
SET status = $2,
    updated_at = now()
WHERE id = $1;

-- name: ListOrderItemsByOrder :many
SELECT id, order_id, product_id, variant_id, title, slug, qty, unit_price, subtotal
FROM order_items
WHERE order_id = $1
ORDER BY title ASC, id;
