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
-- One pass (DISTINCT ON) instead of a correlated subquery per row: there is no index
-- on payments(order_id) and ACCESS EXCLUSIVE is held on refunds here.
UPDATE refunds f
SET payment_id = lp.id
FROM (
  SELECT DISTINCT ON (p.order_id, p.tenant_id) p.id, p.order_id, p.tenant_id
  FROM payments p
  JOIN refunds r ON r.order_id = p.order_id AND r.tenant_id = p.tenant_id AND r.payment_id IS NULL
  ORDER BY p.order_id, p.tenant_id, p.created_at DESC
) lp
WHERE f.payment_id IS NULL AND lp.order_id = f.order_id AND lp.tenant_id = f.tenant_id;

-- Payments fully refunded by the pre-000043 handler have status REFUNDED but
-- refunded_amount 0. Align them with the new rule (REFUNDED <=> refunded_amount equals
-- the paid amount). Not reverted by the down migration: it is a data correction, and
-- 000043 down drops the column.
UPDATE payments p
SET refunded_amount = COALESCE(p.amount, o.pricing_total)
FROM orders o
WHERE o.id = p.order_id AND o.tenant_id = p.tenant_id
  AND p.status = 'REFUNDED' AND p.refunded_amount = 0
  AND COALESCE(p.amount, o.pricing_total) > 0;
