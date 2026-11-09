-- Tenant-scoped voucher writes for the admin API (moved from voucher_rules.sql).

-- name: CreateVoucher :one
INSERT INTO vouchers (code, value, kind, percent_bps, min_spend, usage_limit, valid_from, valid_to, product_ids, category_ids, brand_ids, combinable, priority, per_user_limit, tenant_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
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
  AND tenant_id = $15
RETURNING *;

-- name: AdminVoucherExistsForTenant :one
SELECT EXISTS(
  SELECT 1 FROM vouchers
  WHERE tenant_id = sqlc.arg(tenant_id)
    AND code = sqlc.arg(code)
);
