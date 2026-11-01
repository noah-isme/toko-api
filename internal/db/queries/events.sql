-- name: InsertDomainEvent :one
-- dispatched_at is written as NULL explicitly: that is what makes the row
-- "pending" for the outbox relay. The column default (now()) exists only for
-- binaries that predate migration 000047 and still fan out inline.
-- tenant_id is the emitting request's tenant (NULL when it has none); the
-- outbox relay restores it so a re-dispatch reaches only that tenant's
-- webhook endpoints.
INSERT INTO domain_events (topic, aggregate_id, payload, dispatched_at, tenant_id)
VALUES (sqlc.arg(topic), sqlc.arg(aggregate_id), sqlc.arg(payload), NULL, sqlc.narg(tenant_id))
RETURNING id, topic, aggregate_id, payload, occurred_at, tenant_id, dispatched_at, dispatch_attempts, last_dispatch_error;

-- name: ListDomainEventsByTopic :many
-- Like every query here, it lists all domain_events columns in table order so
-- sqlc maps rows onto dbgen.DomainEvent.
SELECT id, topic, aggregate_id, payload, occurred_at, tenant_id, dispatched_at, dispatch_attempts, last_dispatch_error
FROM domain_events
WHERE topic = $1
ORDER BY occurred_at DESC
LIMIT $2 OFFSET $3;

-- name: MarkDomainEventDispatched :exec
UPDATE domain_events
SET dispatched_at = now(), last_dispatch_error = NULL
WHERE id = $1;

-- name: RecordDomainEventDispatchFailure :exec
UPDATE domain_events
SET dispatch_attempts = dispatch_attempts + 1, last_dispatch_error = $2
WHERE id = $1;

-- name: ListUndispatchedDomainEvents :many
-- Used by the outbox relay. The topic list plus the [not_before, older_than)
-- window let the planner use idx_domain_events_topic_time (topic, occurred_at)
-- as a bounded range scan per topic instead of scanning the append-only table.
SELECT id, topic, aggregate_id, payload, occurred_at, tenant_id, dispatched_at, dispatch_attempts, last_dispatch_error
FROM domain_events
WHERE topic = ANY(sqlc.arg(topics)::text[])
  AND occurred_at >= sqlc.arg(not_before)
  AND occurred_at < sqlc.arg(older_than)
  AND dispatched_at IS NULL
  AND dispatch_attempts < sqlc.arg(max_attempts)
ORDER BY occurred_at ASC
LIMIT sqlc.arg(batch_size);
