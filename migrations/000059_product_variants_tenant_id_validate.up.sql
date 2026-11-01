SET LOCAL lock_timeout = '5s';
-- Per-tenant catalog uniqueness, step 3. VALIDATE takes SHARE UPDATE EXCLUSIVE on
-- product_variants (and ROW SHARE on tenants for the FK): reads and writes continue.
ALTER TABLE product_variants VALIDATE CONSTRAINT product_variants_tenant_id_not_null;
ALTER TABLE product_variants VALIDATE CONSTRAINT product_variants_tenant_id_fkey;
