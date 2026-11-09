SET LOCAL lock_timeout = '5s';
ALTER TABLE shipments
  DROP COLUMN IF EXISTS track_next_poll_at,
  DROP COLUMN IF EXISTS track_attempts,
  DROP COLUMN IF EXISTS track_last_polled_at,
  DROP COLUMN IF EXISTS track_last_error;
