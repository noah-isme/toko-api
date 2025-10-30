-- name: GetVoucherByTenant :one
SELECT id,
       code,
       value,
       kind,
       valid_to
FROM vouchers
WHERE tenant_id = sqlc.arg(tenant_id)
  AND code = sqlc.arg(code)
  AND (valid_to IS NULL OR valid_to > now())
LIMIT 1;
