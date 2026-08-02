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

-- Reserve flash-sale quota at checkout. sold_count includes active order
-- reservations; the update predicate is the concurrency guard.
-- name: ReserveFlashSaleItem :one
WITH updated AS (
  UPDATE flash_sale_items i
  SET sold_count = i.sold_count + sqlc.arg(qty),
      updated_at = now()
  FROM flash_sale_campaigns c
  WHERE i.campaign_id = sqlc.arg(campaign_id)
    AND i.product_id = sqlc.arg(product_id)
    AND c.id = i.campaign_id
    AND c.tenant_id = sqlc.arg(tenant_id)
    AND c.status IN ('SCHEDULED', 'ACTIVE')
    AND c.starts_at <= now()
    AND c.ends_at > now()
    AND (i.stock_limit IS NULL OR i.sold_count + sqlc.arg(qty) <= i.stock_limit)
  RETURNING i.id
)
INSERT INTO flash_sale_order_items (order_id, flash_sale_item_id, qty, status)
SELECT sqlc.arg(order_id), id, sqlc.arg(qty), 'RESERVED'
FROM updated
RETURNING id, order_id, flash_sale_item_id, qty, status, created_at, updated_at;

-- name: CommitFlashSaleReservations :exec
UPDATE flash_sale_order_items
SET status = 'COMMITTED', updated_at = now()
WHERE order_id = sqlc.arg(order_id)
  AND status = 'RESERVED';

-- Release quota reserved by an unpaid, expired, or cancelled order.
-- name: ReleaseFlashSaleReservations :exec
WITH released AS (
  UPDATE flash_sale_order_items
  SET status = 'RELEASED', updated_at = now()
  WHERE order_id = sqlc.arg(order_id)
    AND status = 'RESERVED'
  RETURNING flash_sale_item_id, qty
)
UPDATE flash_sale_items item
SET sold_count = GREATEST(0, item.sold_count - released.qty)
FROM released
WHERE item.id = released.flash_sale_item_id;

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
