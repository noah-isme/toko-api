-- Reverse indexes and columns cautiously
BEGIN;
DROP INDEX IF EXISTS idx_shipments_tenant;
DROP INDEX IF EXISTS idx_payments_tenant;
DROP INDEX IF EXISTS idx_vouchers_tenant;
DROP INDEX IF EXISTS idx_orders_tenant_created;
DROP INDEX IF EXISTS idx_products_tenant;

ALTER TABLE webhook_endpoints DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE shipments        DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE payments         DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE orders           DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE carts            DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE vouchers         DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE brands           DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE categories       DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE products         DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE domain_events    DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE analytics_materialized DROP COLUMN IF EXISTS tenant_id;
COMMIT;
