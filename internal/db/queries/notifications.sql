-- name: CreateNotification :one
INSERT INTO notifications (user_id, tenant_id, type, title, body, data)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, user_id, tenant_id, type, title, body, data, read_at, created_at;

-- name: ListNotifications :many
SELECT id, user_id, tenant_id, type, title, body, data, read_at, created_at
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
