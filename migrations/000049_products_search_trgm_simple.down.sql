SET LOCAL lock_timeout = '5s';
DROP INDEX IF EXISTS idx_products_title_trgm;
DROP INDEX IF EXISTS idx_products_search_simple;
-- pg_trgm is intentionally not dropped: it is a shared extension and other objects may depend on it.
