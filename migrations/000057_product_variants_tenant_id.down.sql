SET LOCAL lock_timeout = '5s';
-- ACCESS EXCLUSIVE on product_variants from the first statement; metadata-only.
-- Dropping the column also drops its FK and CHECK constraints.
DROP TRIGGER IF EXISTS trg_product_variants_tenant_id ON product_variants;
DROP FUNCTION IF EXISTS product_variants_set_tenant_id();
ALTER TABLE product_variants DROP COLUMN IF EXISTS tenant_id;
