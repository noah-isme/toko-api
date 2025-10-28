-- name: CreateUser :one
INSERT INTO users (name, email, password_hash)
VALUES ($1, $2, $3)
RETURNING id, name, email, roles, created_at, updated_at;

-- name: GetUserByEmail :one
SELECT id, name, email, password_hash, roles, created_at, updated_at
FROM users
WHERE email = $1
LIMIT 1;

-- name: GetUserByID :one
SELECT id, name, email, roles, created_at, updated_at
FROM users
WHERE id = $1
LIMIT 1;

-- name: UpdateUserPassword :one
UPDATE users
SET password_hash = $2,
    updated_at    = now()
WHERE id = $1
RETURNING id, name, email, roles, created_at, updated_at;

-- name: UpdateUserProfile :one
UPDATE users
SET name       = $2,
    updated_at = now()
WHERE id = $1
RETURNING id, name, email, roles, created_at, updated_at;
