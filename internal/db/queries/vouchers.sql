-- name: GetVoucherByCode :one
SELECT id, code, value, min_spend, usage_limit, used_count, valid_from, valid_to, product_ids, category_ids, created_at, updated_at
FROM vouchers
WHERE code = $1
LIMIT 1;

-- name: IncreaseVoucherUsedCount :exec
UPDATE vouchers
SET used_count = used_count + 1,
    updated_at = now()
WHERE id = $1
  AND (usage_limit IS NULL OR used_count < usage_limit);
