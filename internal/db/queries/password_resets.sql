-- name: CreatePasswordReset :one
INSERT INTO password_resets (user_id, token, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token, expires_at, used_at, created_at;

-- name: GetPasswordResetByToken :one
SELECT id, user_id, token, expires_at, used_at, created_at
FROM password_resets
WHERE token = $1
LIMIT 1;

-- name: MarkPasswordResetUsed :exec
UPDATE password_resets
SET used_at = now()
WHERE id = $1 AND used_at IS NULL;

-- name: UsePasswordReset :exec
UPDATE password_resets
SET used_at = now()
WHERE token = $1 AND used_at IS NULL;

-- name: DeletePasswordReset :exec
DELETE FROM password_resets
WHERE id = $1;

-- name: DeletePasswordResetsByUser :exec
DELETE FROM password_resets
WHERE user_id = $1;
