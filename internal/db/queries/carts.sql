-- name: CreateCart :one
INSERT INTO carts (user_id, anon_id, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, anon_id, applied_voucher_code, created_at, updated_at, expires_at;

-- name: GetCartByID :one
SELECT id, user_id, anon_id, applied_voucher_code, created_at, updated_at, expires_at
FROM carts
WHERE id = $1
LIMIT 1;

-- name: GetActiveCartByUser :one
SELECT id, user_id, anon_id, applied_voucher_code, created_at, updated_at, expires_at
FROM carts
WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > now())
ORDER BY updated_at DESC
LIMIT 1;

-- name: GetActiveCartByAnon :one
SELECT id, user_id, anon_id, applied_voucher_code, created_at, updated_at, expires_at
FROM carts
WHERE anon_id = $1 AND (expires_at IS NULL OR expires_at > now())
ORDER BY updated_at DESC
LIMIT 1;

-- name: UpdateCartVoucher :exec
UPDATE carts
SET applied_voucher_code = $2,
    updated_at = now()
WHERE id = $1;

-- name: TouchCart :exec
UPDATE carts
SET updated_at = now(),
    expires_at = $2
WHERE id = $1;

-- name: TransferCartToUser :exec
UPDATE carts
SET user_id = $2,
    anon_id = NULL,
    updated_at = now()
WHERE id = $1;
