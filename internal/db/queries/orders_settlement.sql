-- name: ListOrderItemsForStock :many
SELECT product_id, variant_id, qty, slug
FROM order_items
WHERE order_id = $1;

-- name: DecrementVariantStock :exec
UPDATE product_variants
SET stock = GREATEST(0, stock - sqlc.arg(qty))
WHERE id = sqlc.arg(id);

-- name: IncrementVoucherUsageByCode :exec
UPDATE vouchers
SET used_count = used_count + 1
WHERE code = $1
  AND (usage_limit IS NULL OR used_count < usage_limit);
