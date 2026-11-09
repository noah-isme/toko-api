-- name: CreateNotification :one
-- A non-NULL event_id makes the insert idempotent per (event, user): re-running
-- event fan-out (outbox relay) hits the partial unique index from migration
-- 000048 and returns no row (pgx.ErrNoRows) instead of a duplicate.
INSERT INTO notifications (user_id, tenant_id, type, title, body, data, event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (event_id, user_id) WHERE event_id IS NOT NULL DO NOTHING
RETURNING id, user_id, tenant_id, type, title, body, data, read_at, created_at, event_id;

-- name: ListNotifications :many
SELECT id, user_id, tenant_id, type, title, body, data, read_at, created_at, event_id
FROM notifications
WHERE user_id = $1 AND tenant_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;

-- name: CountNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1 AND tenant_id = $2;

-- name: CountUnreadNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1 AND tenant_id = $2 AND read_at IS NULL;

-- name: MarkNotificationRead :one
UPDATE notifications
SET read_at = now()
WHERE id = $1 AND user_id = $2 AND tenant_id = $3 AND read_at IS NULL
RETURNING id;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications
SET read_at = now()
WHERE user_id = $1 AND tenant_id = $2 AND read_at IS NULL;
