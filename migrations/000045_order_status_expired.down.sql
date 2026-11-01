SET LOCAL lock_timeout = '5s';
-- Postgres cannot drop an enum label, so the 'EXPIRED' value is retained (same
-- approach as 000010). Expired orders fall back to the closest previous state.
UPDATE orders SET status = 'CANCELLED', updated_at = now() WHERE status = 'EXPIRED';
