-- name: GetOrderStatus :one
SELECT status FROM orders WHERE id = $1;

-- name: UpdateOrderStatusIfAllowed :one
UPDATE orders
SET status = $2,
    updated_at = now()
WHERE id = $1
  AND (
        (status = 'PENDING_PAYMENT' AND $2 IN ('PAID', 'CANCELED')) OR
        (status = 'PAID' AND $2 IN ('PACKED', 'CANCELED')) OR
        (status = 'PACKED' AND $2 = 'SHIPPED') OR
        (status = 'SHIPPED' AND $2 = 'OUT_FOR_DELIVERY') OR
        (status = 'OUT_FOR_DELIVERY' AND $2 = 'DELIVERED')
      )
RETURNING id;
