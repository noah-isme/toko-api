SET LOCAL lock_timeout = '5s';
-- Partial refunds: a payment can be refunded in several steps. refunded_amount is the
-- cumulative amount the admin refund flow has finalised; status becomes
-- PARTIALLY_REFUNDED while 0 < refunded_amount < paid amount and REFUNDED when equal.
--
-- Native ADD VALUE IF NOT EXISTS (not a DO block) so sqlc sees the label and generates
-- dbgen.PaymentStatusPARTIALLYREFUNDED. PG >= 12 allows it inside the implicit
-- transaction golang-migrate uses, but the new label must not be referenced again in
-- this file (it only becomes usable after commit).
ALTER TYPE payment_status ADD VALUE IF NOT EXISTS 'PARTIALLY_REFUNDED';

ALTER TABLE payments ADD COLUMN IF NOT EXISTS refunded_amount BIGINT NOT NULL DEFAULT 0;

-- NOT VALID: no validation scan while ACCESS EXCLUSIVE is held; 000044 validates it
-- under SHARE UPDATE EXCLUSIVE.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'payments_refunded_amount_nonnegative') THEN
    ALTER TABLE payments ADD CONSTRAINT payments_refunded_amount_nonnegative CHECK (refunded_amount >= 0) NOT VALID;
  END IF;
END $$;
