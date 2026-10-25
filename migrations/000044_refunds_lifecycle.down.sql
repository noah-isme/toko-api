SET LOCAL lock_timeout = '5s';
-- The CHECK validated in 000044 up stays validated; 000043 down drops it.
-- Rows written by the new flow (PENDING/FAILED with provider_ref '') are kept as they are;
-- the pre-000044 handler only inserts/upserts by return_id and ignores them otherwise.
ALTER TABLE refunds DROP COLUMN IF EXISTS payment_id;
ALTER TABLE refunds DROP COLUMN IF EXISTS attempt;
ALTER TABLE refunds DROP COLUMN IF EXISTS updated_at;
ALTER TABLE refunds DROP COLUMN IF EXISTS settled_at;
ALTER TABLE refunds DROP COLUMN IF EXISTS failure_reason;
ALTER TABLE refunds DROP COLUMN IF EXISTS provider_status;
ALTER TABLE refunds ALTER COLUMN provider_ref DROP DEFAULT;
