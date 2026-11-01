-- name: GetVoucherByCode :one
-- Voucher codes are unique per tenant (uq_vouchers_tenant_code), so a code is
-- only ever resolved within one tenant.
SELECT *
FROM vouchers
WHERE tenant_id = sqlc.arg(tenant_id)
  AND code = sqlc.arg(code);

-- name: IncreaseVoucherUsedCount :exec
UPDATE vouchers
SET used_count = used_count + 1,
    updated_at = now()
WHERE id = $1
  AND (usage_limit IS NULL OR used_count < usage_limit);
