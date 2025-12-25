-- PostgreSQL 14+; extend domain tables with tenant_id and indexes; safe backfill then NOT NULL
BEGIN;
-- Add tenant_id columns (adjust table/column names as present in project)
ALTER TABLE products          ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE categories        ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE brands            ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE vouchers          ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE carts             ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE orders            ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE payments          ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE shipments         ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE webhook_endpoints ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE webhook_deliveries ADD COLUMN IF NOT EXISTS tenant_id UUID;
ALTER TABLE domain_events     ADD COLUMN IF NOT EXISTS tenant_id UUID;


-- Backfill with default tenant (by slug 'default')
-- Insert default tenant if not exists
INSERT INTO tenants (name, slug) VALUES ('Default Tenant', 'default') ON CONFLICT (slug) DO NOTHING;

UPDATE products          SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE categories        SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE brands            SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE vouchers          SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE carts             SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE orders            SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE payments          SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE shipments         SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE webhook_endpoints SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE webhook_deliveries SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;
UPDATE domain_events     SET tenant_id=(SELECT id FROM tenants WHERE slug='default') WHERE tenant_id IS NULL;


-- Enforce NOT NULL where relationally required
ALTER TABLE products          ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE categories        ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE brands            ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE vouchers          ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE carts             ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE orders            ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE payments          ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE shipments         ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE webhook_endpoints ALTER COLUMN tenant_id SET NOT NULL;

-- Indexes for common filters/sorts
CREATE INDEX IF NOT EXISTS idx_products_tenant        ON products(tenant_id);
CREATE INDEX IF NOT EXISTS idx_orders_tenant_created  ON orders(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_vouchers_tenant        ON vouchers(tenant_id);
CREATE INDEX IF NOT EXISTS idx_payments_tenant        ON payments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_shipments_tenant       ON shipments(tenant_id);
COMMIT;
