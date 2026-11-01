-- name: GetDomainEvent :one
SELECT id, topic, aggregate_id, payload, occurred_at, tenant_id, dispatched_at, dispatch_attempts, last_dispatch_error
FROM domain_events
WHERE id = $1;
