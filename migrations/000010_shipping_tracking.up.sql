-- +goose Up
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS courier TEXT;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS tracking_number TEXT;
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS last_status shipment_status DEFAULT 'PENDING';
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ;

DO $$
BEGIN
        IF NOT EXISTS (
                SELECT 1
                FROM pg_type t
                JOIN pg_enum e ON t.oid = e.enumtypid
                WHERE t.typname = 'shipment_status' AND e.enumlabel = 'SHIPPED'
        ) THEN
                ALTER TYPE shipment_status ADD VALUE 'SHIPPED';
        END IF;
END
$$;

DO $$
BEGIN
        IF NOT EXISTS (
                SELECT 1
                FROM pg_type t
                JOIN pg_enum e ON t.oid = e.enumtypid
                WHERE t.typname = 'order_status' AND e.enumlabel = 'OUT_FOR_DELIVERY'
        ) THEN
                ALTER TYPE order_status ADD VALUE 'OUT_FOR_DELIVERY';
        END IF;
END
$$;

CREATE TABLE IF NOT EXISTS shipment_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  shipment_id UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
  status shipment_status NOT NULL,
  description TEXT,
  location TEXT,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  raw_payload JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_shipment_events_shipment ON shipment_events(shipment_id);
