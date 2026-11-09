SET LOCAL lock_timeout = '5s';
-- Per-tenant catalog uniqueness, variants: finish product_variants.tenant_id and drop
-- the database-wide SKU constraint now that uq_product_variants_tenant_sku (000060)
-- enforces uniqueness within each tenant.
--
-- Lock footprint: ACCESS EXCLUSIVE on product_variants from the first ALTER (no
-- upgrade). SET NOT NULL skips its table scan because the CHECK validated in 000059
-- proves there are no NULLs, so every statement is metadata-only.
-- Refuse to drop the global constraint unless every per-tenant replacement exists and
-- is valid (a failed CREATE INDEX CONCURRENTLY leaves an INVALID index that
-- IF NOT EXISTS skips). Catalog reads only: no lock on product_variants.
DO $$
DECLARE
  ready int;
BEGIN
  SELECT count(*) INTO ready
  FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
  WHERE i.indrelid = 'product_variants'::regclass
    AND c.relname IN ('uq_product_variants_tenant_sku')
    AND i.indisunique AND i.indisvalid AND i.indisready;
  IF ready <> 1 THEN
    RAISE EXCEPTION 'per-tenant unique index on product_variants is missing or INVALID: rebuild it as in docs/ops/RUNBOOK.md before dropping the global constraint';
  END IF;
END;
$$;
ALTER TABLE product_variants ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE product_variants DROP CONSTRAINT IF EXISTS product_variants_tenant_id_not_null;
ALTER TABLE product_variants DROP CONSTRAINT IF EXISTS product_variants_sku_key;
