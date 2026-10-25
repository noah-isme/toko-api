SET LOCAL lock_timeout = '5s';
-- Validate the CHECK added NOT VALID in 000043 (SHARE UPDATE EXCLUSIVE: does not block writes).
ALTER TABLE payments VALIDATE CONSTRAINT payments_refunded_amount_nonnegative;

-- Refund lifecycle. A refunds row is now written (status PENDING, provider_ref '')
-- before the gateway call so its id gives a stable per-refund gateway key
-- ("rf-<id>-<attempt>"). Only settled_at IS NOT NULL means local records were
-- finalised (money moved); amount may be less than the order total (partial refund).
ALTER TABLE refunds ALTER COLUMN provider_ref SET DEFAULT '';
ALTER TABLE refunds ADD COLUMN IF NOT EXISTS provider_status TEXT;
ALTER TABLE refunds ADD COLUMN IF NOT EXISTS failure_reason TEXT;
ALTER TABLE refunds ADD COLUMN IF NOT EXISTS settled_at TIMESTAMPTZ;
ALTER TABLE refunds ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE refunds ADD COLUMN IF NOT EXISTS attempt INT NOT NULL DEFAULT 0;
-- No FK: refunds must survive payment-row housekeeping; the handler always sets it.
ALTER TABLE refunds ADD COLUMN IF NOT EXISTS payment_id UUID;

-- Pre-existing rows were only ever written after a gateway success.
UPDATE refunds SET settled_at = created_at WHERE provider_ref <> '' AND settled_at IS NULL;
UPDATE refunds f
SET payment_id = (
  SELECT p.id FROM payments p
  WHERE p.order_id = f.order_id AND p.tenant_id = f.tenant_id
  ORDER BY p.created_at DESC
  LIMIT 1
)
WHERE f.payment_id IS NULL;
