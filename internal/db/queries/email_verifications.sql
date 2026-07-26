-- name: CreateEmailVerification :one
INSERT INTO email_verifications (user_id, token, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token, expires_at, used_at, created_at;

-- name: GetEmailVerificationByToken :one
SELECT id, user_id, token, expires_at, used_at, created_at
FROM email_verifications
WHERE token = $1
LIMIT 1;

-- name: UseEmailVerification :exec
UPDATE email_verifications
SET used_at = now()
WHERE token = $1 AND used_at IS NULL;

-- name: DeleteEmailVerificationsByUser :exec
DELETE FROM email_verifications
WHERE user_id = $1;
