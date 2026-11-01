SET LOCAL lock_timeout = '5s';
-- Per-tenant catalog uniqueness, step 2: backfill product_variants.tenant_id for rows
-- written before the 000057 trigger existed.
--
-- Lock footprint: EXCLUSIVE on product_variants for the whole file. Plain reads
-- (storefront, cart and checkout totals) continue; variant writes and row locks
-- (stock reservations, admin edits, FOR NO KEY UPDATE) wait until the file commits.
-- So does every INSERT into a table that references product_variants, because EXCLUSIVE
-- conflicts with the ROW SHARE of the foreign-key check (SELECT ... FOR KEY SHARE):
-- cart_items.variant_id (add-to-cart) and variant alert subscriptions wait for the whole
-- window. The 5 s lock_timeout also means the file waits for any open transaction that
-- has inserted such a row, not only for admin edits, and fails with `lock timeout` if it
-- stays open.
-- The lock is taken before the UPDATE row-locks anything: a weaker lock (ROW
-- EXCLUSIVE, or SHARE ROW EXCLUSIVE, which still admits FOR NO KEY UPDATE) would
-- let a writer that already holds a variant row lock wait on a backfilled row while
-- the backfill waits on its row, which deadlocks and leaves the migration dirty.
-- Nothing later in this file needs a stronger lock, so there is no upgrade.
-- Duration grows linearly with the number of variants; see docs/ops/RUNBOOK.md.
--
-- Safety by default: on a large catalog (more than 50k variants) that still has rows to
-- backfill, fail fast before taking the EXCLUSIVE lock instead of pausing checkout for
-- the whole backfill. Run the batched procedure in docs/ops/RUNBOOK.md first; this file
-- then finds nothing to backfill and passes the guard. The size is a bounded exact count
-- (stops after 50001 rows), not pg_class.reltuples, which is -1 on a never-analyzed table
-- and would let the guard pass silently. Both probes take only ACCESS SHARE.
DO $$
BEGIN
  IF (SELECT count(*) FROM (SELECT 1 FROM product_variants LIMIT 50001) s) > 50000 THEN
    IF EXISTS (SELECT 1 FROM product_variants WHERE tenant_id IS NULL) THEN
      RAISE EXCEPTION 'product_variants has more than 50000 rows and tenant_id is not backfilled: run the batched backfill in docs/ops/RUNBOOK.md (Migrations 000057-000070, large catalogs) before migrating';
    END IF;
  END IF;
END;
$$;
LOCK TABLE product_variants IN EXCLUSIVE MODE;
UPDATE product_variants v
SET tenant_id = p.tenant_id
FROM products p
WHERE p.id = v.product_id
  AND v.tenant_id IS NULL;
