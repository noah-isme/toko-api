SET LOCAL lock_timeout = '5s';
-- Per-tenant catalog uniqueness, step 1 of 14 (000057-000070): give product_variants
-- its own tenant_id so SKUs can be unique per (tenant_id, sku).
--
-- Lock footprint: the first statement takes ACCESS EXCLUSIVE on product_variants
-- (reads and writes wait), so no later statement upgrades a weaker lock. Every
-- statement here is metadata-only (nullable column without default, trigger, NOT VALID
-- constraints), so the window is milliseconds. The FK also takes SHARE ROW EXCLUSIVE
-- on tenants. The backfill runs in 000058 and validation in 000059, outside this lock.
-- Every statement is idempotent, so the file can be applied by hand ahead of the
-- deploy (see docs/ops/RUNBOOK.md, large catalogs) and re-run by golang-migrate.
ALTER TABLE product_variants ADD COLUMN IF NOT EXISTS tenant_id UUID;

-- The variant's tenant is always the product's tenant. Triggers derive it on every
-- insert, on any update of product_id/tenant_id, and on any update of a row whose
-- tenant_id is still NULL, so callers (including the old binaries during the deploy
-- and every INSERT that does not name the column) never set it and can never make it
-- disagree with the product. The last case matters between this file and the 000058
-- backfill: the NOT VALID CHECK below applies to every updated row, so a stock update
-- of a not yet backfilled variant fills it here instead of failing.
--
-- Two triggers, so the hot path stays cheap: the UPDATE trigger's WHEN clause is
-- evaluated in C, and an ordinary stock/price update (checkout reservations) of a row
-- that already has its tenant never enters plpgsql or reads products.
CREATE OR REPLACE FUNCTION product_variants_set_tenant_id() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  SELECT p.tenant_id INTO NEW.tenant_id FROM products p WHERE p.id = NEW.product_id;
  RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_product_variants_tenant_id ON product_variants; -- single trigger of earlier drafts
DROP TRIGGER IF EXISTS trg_product_variants_tenant_id_insert ON product_variants;
CREATE TRIGGER trg_product_variants_tenant_id_insert
  BEFORE INSERT ON product_variants
  FOR EACH ROW EXECUTE FUNCTION product_variants_set_tenant_id();
DROP TRIGGER IF EXISTS trg_product_variants_tenant_id_update ON product_variants;
CREATE TRIGGER trg_product_variants_tenant_id_update
  BEFORE UPDATE ON product_variants
  FOR EACH ROW
  WHEN (NEW.tenant_id IS NULL
        OR NEW.product_id IS DISTINCT FROM OLD.product_id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id)
  EXECUTE FUNCTION product_variants_set_tenant_id();

-- NOT VALID: checked for new and updated rows now, validated without blocking DML in
-- 000059. The CHECK lets 000061 run SET NOT NULL without a table scan under ACCESS
-- EXCLUSIVE. Guarded so the whole file can be re-run: the large-catalog procedure in
-- docs/ops/RUNBOOK.md applies it by hand before the deploy, which then re-runs it.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                 WHERE conrelid = 'product_variants'::regclass AND conname = 'product_variants_tenant_id_fkey') THEN
    ALTER TABLE product_variants
      ADD CONSTRAINT product_variants_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id) NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                 WHERE conrelid = 'product_variants'::regclass AND conname = 'product_variants_tenant_id_not_null') THEN
    ALTER TABLE product_variants
      ADD CONSTRAINT product_variants_tenant_id_not_null CHECK (tenant_id IS NOT NULL) NOT VALID;
  END IF;
END;
$$;
