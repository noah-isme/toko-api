SET LOCAL lock_timeout = '5s';
-- Per-tenant catalog uniqueness, step 2: backfill product_variants.tenant_id for rows
-- written before the 000057 trigger existed.
--
-- Lock footprint: EXCLUSIVE on product_variants for the whole file. Plain reads
-- (storefront, cart, checkout totals) continue; variant writes and row locks
-- (stock reservations, admin edits, FOR NO KEY UPDATE) wait until the file commits.
-- The lock is taken before the UPDATE row-locks anything: a weaker lock (ROW
-- EXCLUSIVE, or SHARE ROW EXCLUSIVE, which still admits FOR NO KEY UPDATE) would
-- let a writer that already holds a variant row lock wait on a backfilled row while
-- the backfill waits on its row, which deadlocks and leaves the migration dirty.
-- Nothing later in this file needs a stronger lock, so there is no upgrade.
-- Duration grows linearly with the number of variants; see docs/ops/RUNBOOK.md.
LOCK TABLE product_variants IN EXCLUSIVE MODE;
UPDATE product_variants v
SET tenant_id = p.tenant_id
FROM products p
WHERE p.id = v.product_id
  AND v.tenant_id IS NULL;
