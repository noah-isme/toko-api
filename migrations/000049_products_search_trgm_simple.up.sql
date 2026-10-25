SET LOCAL lock_timeout = '5s';
-- Search, part 1 of 2: pg_trgm + the stored 'simple' FTS column and its row trigger
-- (expand step of expand/contract). Part 2, 000050, backfills the column and builds the
-- GIN indexes in its own transaction.
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
-- Lock footprint. golang-migrate runs this file as ONE implicit transaction, so every lock
-- below is held until the file commits. ADD COLUMN and SET STORAGE take ACCESS EXCLUSIVE on
-- products (this blocks product reads too) and CREATE TRIGGER takes SHARE ROW EXCLUSIVE.
-- That is why this file contains only catalog-only DDL whose cost does not depend on the
-- number of rows: no UPDATE and no CREATE INDEX (those are in 000050, which never takes
-- ACCESS EXCLUSIVE). The exclusive lock is held for milliseconds; lock_timeout bounds how
-- long the file waits to acquire it.
--
-- Every products DDL statement is guarded by a catalog check, so when the objects already
-- exist (re-run, or the manual procedure in docs/ops/RUNBOOK.md) this file takes no lock on
-- products at all. sqlc does not evaluate DO blocks: the column is declared for code
-- generation in internal/db/schema_sqlc_post.sql.
-- pg_trgm is a trusted extension (PG13+): a database owner can create it without superuser.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- MAIN = keep the value in the heap row (compressed if needed) instead of TOAST. Two tsvectors
-- plus a long description push a row past the ~2 kB TOAST target; a vector left EXTENDED is
-- moved out of line and every sequential-scan match then pays a TOAST fetch (measured ~6x
-- slower on broad terms). Both vectors are set to MAIN so the new binary (simple) and old
-- binaries / app-only rollbacks (english) both keep inline vectors; the toaster moves the
-- description out of line instead. A MAIN value is moved out of line only as a last resort,
-- once the row still exceeds ~8 kB (one page) after that, so rows with very long
-- descriptions get wider. Metadata-only; it applies to rows written from here on, including
-- the backfill in 000050.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_attribute
    WHERE attrelid = 'products'::regclass AND attname = 'search_vector_simple' AND NOT attisdropped
  ) THEN
    ALTER TABLE products ADD COLUMN search_vector_simple tsvector;
  END IF;
  IF EXISTS (
    SELECT 1 FROM pg_attribute
    WHERE attrelid = 'products'::regclass AND attname = 'search_vector_simple'
      AND NOT attisdropped AND attstorage <> 'm'
  ) THEN
    ALTER TABLE products ALTER COLUMN search_vector_simple SET STORAGE MAIN;
  END IF;
  IF EXISTS (
    SELECT 1 FROM pg_attribute
    WHERE attrelid = 'products'::regclass AND attname = 'search_vector'
      AND NOT attisdropped AND attstorage <> 'm'
  ) THEN
    ALTER TABLE products ALTER COLUMN search_vector SET STORAGE MAIN;
  END IF;
END;
$$;

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
