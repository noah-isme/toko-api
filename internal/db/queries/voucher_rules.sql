-- name: CreateVoucher :one
INSERT INTO vouchers (code, value, kind, percent_bps, min_spend, usage_limit, valid_from, valid_to, product_ids, category_ids, brand_ids, combinable, priority, per_user_limit)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: UpdateVoucher :one
UPDATE vouchers
SET value = $2,
    kind = $3,
    percent_bps = $4,
    min_spend = $5,
    usage_limit = $6,
    valid_from = $7,
    valid_to = $8,
    product_ids = $9,
    category_ids = $10,
    brand_ids = $11,
    combinable = $12,
    priority = $13,
    per_user_limit = $14,
    updated_at = now()
WHERE code = $1
RETURNING *;

-- name: GetVoucherByCodeForUpdate :one
SELECT *
FROM vouchers
WHERE code = $1
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
