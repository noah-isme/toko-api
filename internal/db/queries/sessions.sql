-- name: CreateSession :one
INSERT INTO sessions (user_id, refresh_token, user_agent, ip, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, refresh_token, user_agent, ip, expires_at, created_at;

-- name: GetSessionByToken :one
SELECT id, user_id, refresh_token, user_agent, ip, expires_at, created_at
FROM sessions
WHERE refresh_token = $1
LIMIT 1;

-- name: RotateSessionToken :one
UPDATE sessions
SET refresh_token = $2,
    expires_at    = $3
WHERE id = $1
RETURNING id, user_id, refresh_token, user_agent, ip, expires_at, created_at;

-- name: DeleteSessionByToken :exec
DELETE FROM sessions
WHERE refresh_token = $1;

-- name: DeleteSessionsByUser :exec
DELETE FROM sessions
WHERE user_id = $1;
