-- name: CreateWebhookEndpoint :one
INSERT INTO webhook_endpoints (name, url, secret, active, topics)
VALUES (sqlc.arg(name), sqlc.arg(url), sqlc.arg(secret), sqlc.arg(active), sqlc.arg(topics))
RETURNING *;

-- name: UpdateWebhookEndpoint :one
UPDATE webhook_endpoints
SET name = sqlc.arg(name),
    url = sqlc.arg(url),
    secret = sqlc.arg(secret),
    active = sqlc.arg(active),
    topics = sqlc.arg(topics),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetWebhookEndpoint :one
SELECT *
FROM webhook_endpoints
WHERE id = sqlc.arg(id);

-- name: ListWebhookEndpoints :many
SELECT *
FROM webhook_endpoints
ORDER BY created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: DeleteWebhookEndpoint :exec
DELETE FROM webhook_endpoints
WHERE id = sqlc.arg(id);

-- name: ListActiveEndpointsForTopic :many
SELECT *
FROM webhook_endpoints
WHERE active = true
  AND (coalesce(array_length(topics, 1), 0) = 0 OR sqlc.arg(topic)::text = ANY(topics))
ORDER BY created_at ASC;

-- name: EnqueueDelivery :one
INSERT INTO webhook_deliveries (endpoint_id, event_id, status, max_attempt, next_attempt_at)
VALUES (sqlc.arg(endpoint_id), sqlc.arg(event_id), 'PENDING', sqlc.arg(max_attempt), now())
RETURNING *;

-- name: DequeueDueDeliveries :many
SELECT *
FROM webhook_deliveries
WHERE status IN ('PENDING', 'FAILED')
  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
ORDER BY next_attempt_at NULLS FIRST, created_at ASC
LIMIT $1;

-- name: MarkDelivering :exec
UPDATE webhook_deliveries
SET status = 'DELIVERING', updated_at = now()
WHERE id = sqlc.arg(id);

-- name: MarkDelivered :exec
UPDATE webhook_deliveries
SET status = 'DELIVERED',
    response_status = sqlc.arg(response_status),
    response_body = sqlc.arg(response_body),
    last_error = NULL,
    next_attempt_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: MarkFailedWithBackoff :exec
UPDATE webhook_deliveries
SET status = 'FAILED',
    attempt = attempt + 1,
    next_attempt_at = now() + (sqlc.arg(delay_sec)::int * interval '1 second'),
    last_error = sqlc.arg(last_error),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: MoveToDLQ :exec
UPDATE webhook_deliveries
SET status = 'DLQ',
    last_error = sqlc.arg(last_error),
    next_attempt_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: InsertWebhookDlq :one
INSERT INTO webhook_dlq (delivery_id, reason)
VALUES (sqlc.arg(delivery_id), sqlc.arg(reason))
RETURNING *;

-- name: GetDeliveryByID :one
SELECT *
FROM webhook_deliveries
WHERE id = sqlc.arg(id);

-- name: ResetDeliveryForReplay :one
UPDATE webhook_deliveries
SET status = 'PENDING',
    attempt = 0,
    next_attempt_at = now(),
    last_error = NULL,
    response_status = NULL,
    response_body = NULL,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDlqByDelivery :exec
DELETE FROM webhook_dlq
WHERE delivery_id = $1;

-- name: ListWebhookDeliveries :many
SELECT wd.*, we.name AS endpoint_name, we.url AS endpoint_url, we.active AS endpoint_active
FROM webhook_deliveries wd
JOIN webhook_endpoints we ON we.id = wd.endpoint_id
WHERE (sqlc.arg(endpoint_id)::uuid IS NULL OR wd.endpoint_id = sqlc.arg(endpoint_id)::uuid)
  AND (sqlc.arg(event_id)::uuid IS NULL OR wd.event_id = sqlc.arg(event_id)::uuid)
  AND (sqlc.arg(status)::text IS NULL OR sqlc.arg(status)::text = '' OR wd.status = sqlc.arg(status)::delivery_status)
ORDER BY wd.created_at DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountWebhookDeliveries :one
SELECT count(*)
FROM webhook_deliveries wd
WHERE (sqlc.arg(endpoint_id)::uuid IS NULL OR wd.endpoint_id = sqlc.arg(endpoint_id)::uuid)
  AND (sqlc.arg(event_id)::uuid IS NULL OR wd.event_id = sqlc.arg(event_id)::uuid)
  AND (sqlc.arg(status)::text IS NULL OR sqlc.arg(status)::text = '' OR wd.status = sqlc.arg(status)::delivery_status);
