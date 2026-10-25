SET LOCAL lock_timeout = '5s';
-- Search: pg_trgm typo tolerance + 'simple' FTS config (expand step of expand/contract).
-- The english search_vector column and idx_products_search stay until a later release:
-- old binaries query them during the deploy window and after an app-only rollback.
-- FOLLOW-UP (later release): drop products.search_vector and idx_products_search.
-- Runs as one implicit transaction; holds SHARE on products during the two index builds
-- (reads continue, product writes wait up to lock_timeout). On a large catalog, pre-build
-- each index by hand with CREATE INDEX CONCURRENTLY IF NOT EXISTS <same name> ...; this
-- file then becomes a no-op. pg_trgm is a trusted extension (PG13+): a database owner can
-- create it without superuser.
-- The index expression below must stay character-for-character identical to the one used in
-- internal/db/queries/products.sql, or the planner will not use the index.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_products_search_simple ON products USING GIN (
  (setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
   setweight(to_tsvector('simple', coalesce(description, '')), 'B'))
);
CREATE INDEX IF NOT EXISTS idx_products_title_trgm ON products USING GIN (title gin_trgm_ops);
