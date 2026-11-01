SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS product_sales;
-- The backfilled search_vector_simple values stay; 000049's down drops the column.
DROP INDEX IF EXISTS idx_products_title_trgm;
DROP INDEX IF EXISTS idx_products_search_simple;
