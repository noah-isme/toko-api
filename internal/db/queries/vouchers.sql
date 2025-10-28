-- name: GetVoucherByCode :one
SELECT *
FROM vouchers
WHERE code = $1
LIMIT 1;

-- name: IncreaseVoucherUsedCount :exec
UPDATE vouchers
SET used_count = used_count + 1,
    updated_at = now()
WHERE id = $1
  AND (usage_limit IS NULL OR used_count < usage_limit);
