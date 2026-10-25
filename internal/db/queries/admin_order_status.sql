-- Tenant-scoped order status transitions for the admin API.

-- name: AdminGetOrderStatus :one
SELECT status
FROM orders
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id);

-- name: AdminUpdateOrderStatusIfAllowed :one
UPDATE orders
SET status = sqlc.arg(status)::order_status,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND (
        (orders.status = 'PENDING_PAYMENT' AND sqlc.arg(status)::order_status IN ('PAID', 'CANCELLED')) OR
        (orders.status = 'PAID' AND sqlc.arg(status)::order_status IN ('PACKED', 'CANCELLED')) OR
        (orders.status = 'PACKED' AND sqlc.arg(status)::order_status = 'SHIPPED') OR
        (orders.status = 'SHIPPED' AND sqlc.arg(status)::order_status = 'OUT_FOR_DELIVERY') OR
        (orders.status = 'OUT_FOR_DELIVERY' AND sqlc.arg(status)::order_status = 'DELIVERED')
      )
RETURNING id;
