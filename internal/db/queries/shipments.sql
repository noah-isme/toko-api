-- name: CreateShipment :one
INSERT INTO shipments (order_id, status, courier, tracking_number, history, last_status, last_event_at)
VALUES ($1, 'PENDING', $2, $3, '[]'::jsonb, 'PENDING', now())
RETURNING id, order_id, status, courier, tracking_number, history, last_status, last_event_at;

-- name: GetShipmentByOrder :one
SELECT id, order_id, status, courier, tracking_number, history, last_status, last_event_at
FROM shipments
WHERE order_id = $1
LIMIT 1;

-- name: UpdateShipmentStatus :one
UPDATE shipments
SET status = $2,
    last_status = $2,
    last_event_at = now()
WHERE id = $1
RETURNING id;

-- name: InsertShipmentEvent :one
INSERT INTO shipment_events (shipment_id, status, description, location, occurred_at, raw_payload)
VALUES (sqlc.arg(shipment_id), sqlc.arg(status), sqlc.arg(description), sqlc.arg(location), COALESCE(sqlc.arg(occurred_at)::timestamptz, now()), sqlc.arg(raw_payload))
RETURNING id, shipment_id, status, description, location, occurred_at, raw_payload, created_at;

-- name: ListShipmentEvents :many
SELECT id, shipment_id, status, description, location, occurred_at, raw_payload, created_at
FROM shipment_events
WHERE shipment_id = $1
ORDER BY occurred_at ASC, created_at ASC;

-- Shipments the tracking job should poll now, oldest schedule first.
-- name: ListShipmentsDueForTracking :many
SELECT id, order_id, tenant_id, courier, tracking_number, status, last_status, last_event_at, track_attempts
FROM shipments
WHERE tenant_id = sqlc.arg(tenant_id)
  AND COALESCE(last_status, status) IN ('SHIPPED', 'OUT_FOR_DELIVERY')
  AND COALESCE(tracking_number, '') <> ''
  AND track_next_poll_at IS NOT NULL
  AND track_next_poll_at <= now()
  AND COALESCE(last_event_at, now()) >= now() - make_interval(days => sqlc.arg(max_age_days)::int)
ORDER BY track_next_poll_at, id
LIMIT sqlc.arg(batch_size);

-- name: UpdateShipmentTrackingPoll :exec
UPDATE shipments
SET track_attempts = sqlc.arg(track_attempts),
    track_next_poll_at = sqlc.narg(track_next_poll_at),
    track_last_polled_at = now(),
    track_last_error = sqlc.narg(track_last_error)
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id);

-- Stops polling shipments whose last carrier event is older than the max age.
-- name: StopTrackingStaleShipments :execrows
UPDATE shipments
SET track_next_poll_at = NULL,
    track_last_error = 'stopped: max_age'
WHERE tenant_id = sqlc.arg(tenant_id)
  AND track_next_poll_at IS NOT NULL
  AND COALESCE(last_status, status) IN ('SHIPPED', 'OUT_FOR_DELIVERY')
  AND COALESCE(last_event_at, now()) < now() - make_interval(days => sqlc.arg(max_age_days)::int);
