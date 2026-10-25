-- name: GetOrderStatus :one
SELECT status FROM orders WHERE id = $1;

-- The status guard. The parameter is cast explicitly: without the cast
-- PostgreSQL infers it as text from the IN lists and rejects the SET.
-- name: UpdateOrderStatusIfAllowed :one
UPDATE orders
SET status = sqlc.arg(status)::order_status,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND (
        (status = 'PENDING_PAYMENT' AND sqlc.arg(status)::order_status IN ('PAID', 'CANCELLED', 'EXPIRED')) OR
        (status = 'PAID' AND sqlc.arg(status)::order_status IN ('PACKED', 'CANCELLED')) OR
        (status = 'PACKED' AND sqlc.arg(status)::order_status = 'SHIPPED') OR
        (status = 'SHIPPED' AND sqlc.arg(status)::order_status = 'OUT_FOR_DELIVERY') OR
        (status = 'OUT_FOR_DELIVERY' AND sqlc.arg(status)::order_status = 'DELIVERED')
      )
RETURNING id;
