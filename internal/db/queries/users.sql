-- name: CreateUser :one
INSERT INTO users (name, email, password_hash)
VALUES ($1, $2, $3)
RETURNING id, name, email, phone, roles, email_verified_at, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, phone, password_hash, roles, email_verified_at, created_at, updated_at
FROM users
WHERE email = $1
LIMIT 1;

-- name: GetUserByID :one
SELECT id, name, email, phone, roles, email_verified_at, created_at, updated_at
FROM users
WHERE id = $1
LIMIT 1;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = $2,
    updated_at    = now()
WHERE id = $1
RETURNING id, name, email, phone, roles, email_verified_at, created_at, updated_at;

-- name: UpdateUserProfile :one
UPDATE users
SET name       = COALESCE(sqlc.narg('name'), name),
    phone      = COALESCE(sqlc.narg('phone'), phone),
    updated_at = now()
WHERE id = sqlc.arg('id')
RETURNING id, name, email, phone, roles, email_verified_at, created_at, updated_at;

-- name: MarkUserEmailVerified :one
UPDATE users
SET email_verified_at = COALESCE(email_verified_at, now()),
    updated_at        = now()
WHERE id = $1
RETURNING id, name, email, phone, roles, email_verified_at, created_at, updated_at;
