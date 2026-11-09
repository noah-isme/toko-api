-- name: GetVoucherByCodeForUpdate :one
-- Voucher codes are unique per tenant (uq_vouchers_tenant_code).
SELECT *
FROM vouchers
WHERE tenant_id = sqlc.arg(tenant_id)
  AND code = sqlc.arg(code)
FOR UPDATE;

-- name: CountVoucherUsageByUser :one
SELECT COUNT(*)
FROM voucher_usages
WHERE voucher_id = $1
  AND user_id = $2;

-- name: InsertVoucherUsage :exec
INSERT INTO voucher_usages (voucher_id, user_id, order_id, amount)
VALUES ($1, $2, $3, $4)
ON CONFLICT (voucher_id, order_id) DO NOTHING;

-- name: GetVoucherUsageByOrder :one
SELECT id, voucher_id, user_id, order_id, used_at, amount
FROM voucher_usages
WHERE voucher_id = $1
  AND order_id = $2
LIMIT 1;
