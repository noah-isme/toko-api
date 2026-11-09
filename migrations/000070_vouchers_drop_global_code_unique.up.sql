SET LOCAL lock_timeout = '5s';
-- Per-tenant catalog uniqueness: drop the database-wide unique constraint(s) on vouchers,
-- now that the per-tenant index(es) built by the previous migration(s) enforce
-- uniqueness within each tenant. DROP CONSTRAINT takes ACCESS EXCLUSIVE on vouchers
-- as its first lock (no upgrade) and is metadata-only, so the window is milliseconds.
-- Refuse to drop the global constraint unless every per-tenant replacement exists and
-- is valid (a failed CREATE INDEX CONCURRENTLY leaves an INVALID index that
-- IF NOT EXISTS skips). Catalog reads only: no lock on vouchers.
DO $$
DECLARE
  ready int;
BEGIN
  SELECT count(*) INTO ready
  FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
  WHERE i.indrelid = 'vouchers'::regclass
    AND c.relname IN ('uq_vouchers_tenant_code')
    AND i.indisunique AND i.indisvalid AND i.indisready;
  IF ready <> 1 THEN
    RAISE EXCEPTION 'per-tenant unique index on vouchers is missing or INVALID: rebuild it as in docs/ops/RUNBOOK.md before dropping the global constraint';
  END IF;
END;
$$;
ALTER TABLE vouchers DROP CONSTRAINT IF EXISTS vouchers_code_key;
