SET LOCAL lock_timeout = '5s';
-- Search, part 2 of 2 (backfill + indexes), then the product_sales table.
--
-- Lock footprint. This file runs as ONE implicit transaction, separate from 000049, and
-- never takes ACCESS EXCLUSIVE on products: the backfill UPDATE takes ROW EXCLUSIVE and
-- row-locks the rows it fills, the two GIN builds take SHARE, and the product_sales FK takes
-- SHARE ROW EXCLUSIVE, all held until commit. Product READS (storefront list/detail, cart,
-- checkout) continue throughout; product WRITES (stock flag updates, admin catalog edits)
-- wait until the file commits. Measured ~5 s on a 20k-product tenant with ~600-character
-- descriptions; the cost grows linearly with the catalog. Above ~20k products run the
-- batched, concurrent procedure in docs/ops/RUNBOOK.md before the deploy: this file then
-- finds both indexes, skips the backfill and builds nothing.
--
-- The backfill runs only while idx_products_search_simple does not exist yet. The manual
-- procedure builds that index only after its batched backfill reports UPDATE 0, and the
-- 000049 trigger fills the column on every insert/update from then on, so an existing valid
-- index means there is nothing left to backfill. An INVALID index (a failed
-- CREATE INDEX CONCURRENTLY) aborts the file instead of being silently kept or rebuilt
-- under lock; see the RUNBOOK for the recovery.
DO $$
DECLARE
  simple_valid boolean;
  trgm_valid   boolean;
BEGIN
  SELECT i.indisvalid INTO simple_valid
  FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
  WHERE i.indrelid = 'products'::regclass AND c.relname = 'idx_products_search_simple';
  SELECT i.indisvalid INTO trgm_valid
  FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
  WHERE i.indrelid = 'products'::regclass AND c.relname = 'idx_products_title_trgm';

  IF simple_valid IS FALSE OR trgm_valid IS FALSE THEN
    RAISE EXCEPTION 'products search index is INVALID (failed CREATE INDEX CONCURRENTLY): DROP INDEX CONCURRENTLY it and rebuild it as in docs/ops/RUNBOOK.md, then re-run the migration';
  END IF;

  IF simple_valid IS NULL THEN
    -- Rows written before the 000049 trigger existed.
    UPDATE products
    SET search_vector_simple =
          setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
          setweight(to_tsvector('simple', coalesce(description, '')), 'B')
    WHERE search_vector_simple IS NULL;
    CREATE INDEX idx_products_search_simple ON products USING GIN (search_vector_simple);
  END IF;
  IF trgm_valid IS NULL THEN
    CREATE INDEX idx_products_title_trgm ON products USING GIN (title gin_trgm_ops);
  END IF;
END;
$$;

-- product_sales: derived per-product counter of units sold in paid orders, used by the
-- storefront popularity sort. It is written ONLY by the worker's periodic refresh job
-- (internal/catalog/popularity.go), which recomputes it per tenant from order_items JOIN
-- orders; popularity may therefore be up to one refresh interval stale. Deliberately no
-- triggers: keeping the counter out of the payment-settlement transaction means paid
-- webhooks never contend on a shared row (see .plans/rec-api-search.md, Decision 5/15).
-- No backfill here (the first worker run fills the table) and no lock on orders/order_items.
CREATE TABLE IF NOT EXISTS product_sales (
  product_id UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  units_sold BIGINT NOT NULL DEFAULT 0 CHECK (units_sold >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_product_sales_tenant ON product_sales(tenant_id);
