-- name: ListAddressesByUser :many
SELECT id, user_id, label, receiver_name, phone, country, province, city,
       postal_code, address_line1, address_line2, is_default, created_at, updated_at
FROM addresses
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountAddressesByUser :one
SELECT COUNT(*)
FROM addresses
WHERE user_id = $1;

-- name: GetAddressByID :one
SELECT id, user_id, label, receiver_name, phone, country, province, city,
       postal_code, address_line1, address_line2, is_default, created_at, updated_at
FROM addresses
WHERE id = $1 AND user_id = $2
LIMIT 1;

-- name: CreateAddress :one
INSERT INTO addresses (
    user_id,
    label,
    receiver_name,
    phone,
    country,
    province,
    city,
    postal_code,
    address_line1,
    address_line2,
    is_default
) VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8,
    $9,
    $10,
    $11
)
RETURNING id, user_id, label, receiver_name, phone, country, province, city,
          postal_code, address_line1, address_line2, is_default, created_at, updated_at;

-- name: UpdateAddress :one
UPDATE addresses
SET label         = $3,
    receiver_name = $4,
    phone         = $5,
    country       = $6,
    province      = $7,
    city          = $8,
    postal_code   = $9,
    address_line1 = $10,
    address_line2 = $11,
    is_default    = $12,
    updated_at    = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, label, receiver_name, phone, country, province, city,
          postal_code, address_line1, address_line2, is_default, created_at, updated_at;

-- name: DeleteAddress :exec
DELETE FROM addresses
WHERE id = $1 AND user_id = $2;

-- name: UnsetDefaultAddresses :exec
UPDATE addresses
SET is_default = FALSE,
    updated_at = now()
WHERE user_id = sqlc.arg(user_id)
  AND (sqlc.arg(exclude_id)::uuid IS NULL OR id <> sqlc.arg(exclude_id))
  AND is_default;
