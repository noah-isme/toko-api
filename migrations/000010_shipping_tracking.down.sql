-- +goose Down
DROP TABLE IF EXISTS shipment_events;
ALTER TABLE shipments DROP COLUMN IF EXISTS last_event_at;
ALTER TABLE shipments DROP COLUMN IF EXISTS last_status;
ALTER TABLE shipments DROP COLUMN IF EXISTS tracking_number;
ALTER TABLE shipments DROP COLUMN IF EXISTS courier;
