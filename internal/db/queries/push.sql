-- name: GetPushSubscription :one
SELECT id, user_id, endpoint, p256dh, auth, created_at, updated_at
FROM push_subscriptions
WHERE user_id = $1 AND endpoint = $2;

-- name: GetPushSubscriptionsByUser :many
SELECT id, user_id, endpoint, p256dh, auth, created_at, updated_at
FROM push_subscriptions
WHERE user_id = $1;

-- name: CreatePushSubscription :one
INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, endpoint) DO UPDATE SET
    p256dh = EXCLUDED.p256dh,
    auth = EXCLUDED.auth,
    updated_at = NOW()
RETURNING id, user_id, endpoint, p256dh, auth, created_at, updated_at;

-- name: DeletePushSubscription :exec
DELETE FROM push_subscriptions
WHERE user_id = $1 AND endpoint = $2;

-- name: DeleteAllPushSubscriptions :exec
DELETE FROM push_subscriptions WHERE user_id = $1;

-- name: GetPushPreferences :one
SELECT user_id, enabled, order_updates, promo_updates, stock_updates, updated_at
FROM push_preferences
WHERE user_id = $1;

-- name: UpsertPushPreferences :one
INSERT INTO push_preferences (user_id, enabled, order_updates, promo_updates, stock_updates)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (user_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    order_updates = EXCLUDED.order_updates,
    promo_updates = EXCLUDED.promo_updates,
    stock_updates = EXCLUDED.stock_updates,
    updated_at = NOW()
RETURNING user_id, enabled, order_updates, promo_updates, stock_updates, updated_at;
