-- name: SubscribeNewsletter :one
INSERT INTO newsletter_subscribers (tenant_id, email, status, updated_at)
VALUES ($1, $2, 'SUBSCRIBED', now())
ON CONFLICT (tenant_id, email) DO UPDATE SET
    status = 'SUBSCRIBED',
    updated_at = now()
RETURNING id, tenant_id, email, status, created_at, updated_at;

-- name: UnsubscribeNewsletter :exec
UPDATE newsletter_subscribers
SET status = 'UNSUBSCRIBED', updated_at = now()
WHERE tenant_id = $1 AND email = $2;

-- name: GetNewsletterSubscriber :one
SELECT id, tenant_id, email, status, created_at, updated_at
FROM newsletter_subscribers
WHERE tenant_id = $1 AND email = $2
LIMIT 1;

-- name: ListNewsletterSubscribers :many
SELECT id, tenant_id, email, status, created_at, updated_at
FROM newsletter_subscribers
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountNewsletterSubscribers :one
SELECT count(*)
FROM newsletter_subscribers
WHERE tenant_id = $1 AND status = 'SUBSCRIBED';
