SET LOCAL lock_timeout = '5s';
DROP INDEX IF EXISTS idx_products_title_trgm;
DROP INDEX IF EXISTS idx_products_search_simple;
DROP TRIGGER IF EXISTS trg_products_search_vector_simple ON products;
DROP FUNCTION IF EXISTS products_search_vector_simple_refresh();
ALTER TABLE products DROP COLUMN IF EXISTS search_vector_simple;
ALTER TABLE products ALTER COLUMN search_vector SET STORAGE EXTENDED;
-- pg_trgm is intentionally not dropped: it is a shared extension and other objects may depend on it.
