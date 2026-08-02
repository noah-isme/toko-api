-- name: ListOrderItemsForStock :many
SELECT product_id, variant_id, qty, slug
FROM order_items
WHERE order_id = $1;

-- name: DecrementVariantStock :exec
UPDATE product_variants
SET stock = GREATEST(0, stock - sqlc.arg(qty))
WHERE id = sqlc.arg(id);

-- Reserve stock at order creation. The predicate makes concurrent checkouts
-- fail instead of silently overselling the last units.
-- name: ReserveVariantStock :one
UPDATE product_variants
SET stock = stock - sqlc.arg(qty)
WHERE id = sqlc.arg(id)
  AND stock >= sqlc.arg(qty)
RETURNING id;

-- name: ReleaseVariantStock :exec
UPDATE product_variants
SET stock = stock + sqlc.arg(qty)
WHERE id = sqlc.arg(id);

-- name: CreateInventoryReservation :one
INSERT INTO inventory_reservations (tenant_id, order_id, product_id, variant_id, qty, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListInventoryReservationsByOrder :many
SELECT *
FROM inventory_reservations
WHERE order_id = $1
ORDER BY id;

-- name: ListExpiredInventoryReservationsForTenant :many
SELECT *
FROM inventory_reservations
WHERE tenant_id = $1
  AND status = 'RESERVED'
  AND expires_at <= now()
FOR UPDATE;

-- name: TransitionInventoryReservation :one
UPDATE inventory_reservations
SET status = sqlc.arg(to_status), updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status)
RETURNING *;

-- name: IncrementVoucherUsageByCode :exec
UPDATE vouchers
SET used_count = used_count + 1
WHERE code = $1
  AND (usage_limit IS NULL OR used_count < usage_limit);
