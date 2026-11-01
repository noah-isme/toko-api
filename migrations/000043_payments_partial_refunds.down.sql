SET LOCAL lock_timeout = '5s';
-- PostgreSQL cannot drop an enum label, so 'PARTIALLY_REFUNDED' stays on payment_status
-- after this down migration (the up migration uses ADD VALUE IF NOT EXISTS, so a re-run
-- is safe). Rows using it are reset to PAID, the last status the old code understands.
UPDATE payments SET status = 'PAID' WHERE status = 'PARTIALLY_REFUNDED';
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_refunded_amount_nonnegative;
ALTER TABLE payments DROP COLUMN IF EXISTS refunded_amount;
