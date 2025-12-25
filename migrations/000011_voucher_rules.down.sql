DROP INDEX IF EXISTS idx_voucher_usages_order;
DROP INDEX IF EXISTS idx_voucher_usages_user;
DROP INDEX IF EXISTS idx_voucher_usages_voucher;
DROP TABLE IF EXISTS voucher_usages;

ALTER TABLE orders DROP COLUMN IF EXISTS applied_voucher_code;

ALTER TABLE vouchers
    DROP COLUMN IF EXISTS brand_ids,
    DROP COLUMN IF EXISTS per_user_limit,
    DROP COLUMN IF EXISTS priority,
    DROP COLUMN IF EXISTS combinable,
    DROP COLUMN IF EXISTS percent_bps,
    DROP COLUMN IF EXISTS kind;

DROP TYPE IF EXISTS discount_kind;
