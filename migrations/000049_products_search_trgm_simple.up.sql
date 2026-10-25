SET LOCAL lock_timeout = '5s';
-- Search: pg_trgm typo tolerance + 'simple' FTS config (expand step of expand/contract).
-- The english search_vector column and idx_products_search stay until a later release:
-- old binaries query them during the deploy window and after an app-only rollback.
-- FOLLOW-UP (later release): drop products.search_vector and idx_products_search.
--
-- search_vector_simple is a plain nullable column (ADD COLUMN without a default is
-- metadata-only, no table rewrite) kept current by a BEFORE INSERT/UPDATE OF title,
-- description row trigger, so it is filled for writes from old and new binaries alike.
-- Storing it (instead of an expression index) keeps ranking and sequential scans as cheap as
-- the old stored english search_vector: broad terms such as "dan" otherwise rebuild two
-- tsvectors per row (measured ~400 ms vs ~20 ms per count on a 20k-product tenant, see RUNBOOK).
--
-- Lock footprint, one implicit transaction: ADD COLUMN and CREATE TRIGGER take brief
-- ACCESS EXCLUSIVE / SHARE ROW EXCLUSIVE locks (bounded by lock_timeout); the backfill
-- UPDATE row-locks every product until commit; the two index builds hold SHARE on products
-- (reads continue, product writes wait). On a large catalog run the batched, concurrent
-- procedure in docs/ops/RUNBOOK.md first; every statement below is idempotent, so this file
-- then finds nothing to backfill and no index to build.
-- pg_trgm is a trusted extension (PG13+): a database owner can create it without superuser.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE products ADD COLUMN IF NOT EXISTS search_vector_simple tsvector;
-- MAIN = keep the value in the heap row (compressed if needed) instead of TOAST. Two tsvectors
-- plus a long description push a row past the 2 kB TOAST threshold; a vector left EXTENDED is
-- moved out of line and every sequential-scan match then pays a TOAST fetch (measured ~6x
-- slower on broad terms). Both vectors are set to MAIN so the new binary (simple) and old
-- binaries / app-only rollbacks (english) both keep inline vectors; the toaster moves the
-- description out of line instead. Metadata-only; it applies to rows written from here on,
-- including the backfill UPDATE below, which rewrites every existing row.
ALTER TABLE products ALTER COLUMN search_vector_simple SET STORAGE MAIN;
ALTER TABLE products ALTER COLUMN search_vector SET STORAGE MAIN;

CREATE OR REPLACE FUNCTION products_search_vector_simple_refresh() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.search_vector_simple :=
    setweight(to_tsvector('simple', coalesce(NEW.title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(NEW.description, '')), 'B');
  RETURN NEW;
END;
$$;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_trigger
    WHERE tgname = 'trg_products_search_vector_simple' AND tgrelid = 'products'::regclass
  ) THEN
    CREATE TRIGGER trg_products_search_vector_simple
      BEFORE INSERT OR UPDATE OF title, description ON products
      FOR EACH ROW EXECUTE FUNCTION products_search_vector_simple_refresh();
  END IF;
END;
$$;

-- Backfill rows written before the trigger existed (no-op after a manual batched backfill).
UPDATE products
SET search_vector_simple =
      setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
      setweight(to_tsvector('simple', coalesce(description, '')), 'B')
WHERE search_vector_simple IS NULL;

CREATE INDEX IF NOT EXISTS idx_products_search_simple ON products USING GIN (search_vector_simple);
CREATE INDEX IF NOT EXISTS idx_products_title_trgm ON products USING GIN (title gin_trgm_ops);
