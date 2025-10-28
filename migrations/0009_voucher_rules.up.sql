CREATE TYPE discount_kind AS ENUM ('fixed_amount', 'percent');

ALTER TABLE vouchers
    ADD COLUMN IF NOT EXISTS kind discount_kind DEFAULT 'fixed_amount',
    ADD COLUMN IF NOT EXISTS percent_bps INT,
    ADD COLUMN IF NOT EXISTS combinable BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS priority INT NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS per_user_limit INT,
    ADD COLUMN IF NOT EXISTS brand_ids UUID[] DEFAULT '{}';

ALTER TABLE vouchers
    ALTER COLUMN kind SET NOT NULL,
    ALTER COLUMN combinable SET DEFAULT false,
    ALTER COLUMN priority SET DEFAULT 100;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS applied_voucher_code TEXT;

CREATE TABLE IF NOT EXISTS voucher_usages (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  voucher_id UUID NOT NULL REFERENCES vouchers(id) ON DELETE CASCADE,
  user_id UUID,
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  used_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  amount BIGINT NOT NULL,
  UNIQUE (voucher_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_voucher_usages_voucher ON voucher_usages(voucher_id);
CREATE INDEX IF NOT EXISTS idx_voucher_usages_user ON voucher_usages(user_id);
CREATE INDEX IF NOT EXISTS idx_voucher_usages_order ON voucher_usages(order_id);
