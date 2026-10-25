-- name: InsertDomainEvent :one
INSERT INTO domain_events (topic, aggregate_id, payload, tenant_id)
VALUES (sqlc.arg(topic), sqlc.arg(aggregate_id), sqlc.arg(payload), sqlc.narg(tenant_id))
RETURNING id, topic, aggregate_id, payload, occurred_at;

-- name: ListDomainEventsByTopic :many
SELECT id, topic, aggregate_id, payload, occurred_at
FROM domain_events
WHERE topic = $1
ORDER BY occurred_at DESC
LIMIT $2 OFFSET $3;
