-- name: GetDomainEvent :one
SELECT id, topic, aggregate_id, payload, occurred_at
FROM domain_events
WHERE id = $1;
