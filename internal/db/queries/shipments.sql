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
