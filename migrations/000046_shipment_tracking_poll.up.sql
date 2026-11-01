SET LOCAL lock_timeout = '5s';
-- Polling state for the shipment-tracking worker job. track_next_poll_at NULL
-- means polling has stopped for the shipment. Constant defaults are
-- metadata-only on PostgreSQL 11+, so the table is not rewritten.
ALTER TABLE shipments
  ADD COLUMN IF NOT EXISTS track_next_poll_at TIMESTAMPTZ DEFAULT now(),
  ADD COLUMN IF NOT EXISTS track_attempts INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS track_last_polled_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS track_last_error TEXT;
