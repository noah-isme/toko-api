SET LOCAL lock_timeout = '5s';
-- Restores the database-wide SKU constraint and the pre-000061 nullability. Builds
-- the SKU index under ACCESS EXCLUSIVE (reads and writes on product_variants wait).
--
-- This down FAILS with a unique violation ("could not create unique index") once two
-- tenants use the same SKU, which per-tenant uniqueness allows. Find the clashes
-- with the query in docs/ops/RUNBOOK.md ("Migrations 000057-000070"), rename one side,
-- then run the down again. Nothing is changed when it fails (one transaction), but
-- golang-migrate leaves the version dirty at 60: clear it with `migrate force 61` first.
ALTER TABLE product_variants ADD CONSTRAINT product_variants_sku_key UNIQUE (sku);
ALTER TABLE product_variants ALTER COLUMN tenant_id DROP NOT NULL;
-- NOT VALID (no scan under the lock): every row satisfies it and the 000057 trigger
-- fills new rows. Re-running 000061 up then scans once for SET NOT NULL.
ALTER TABLE product_variants
  ADD CONSTRAINT product_variants_tenant_id_not_null CHECK (tenant_id IS NOT NULL) NOT VALID;
