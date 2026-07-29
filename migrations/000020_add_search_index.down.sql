DROP INDEX IF EXISTS idx_products_search;

ALTER TABLE products DROP COLUMN IF EXISTS search_vector;

ALTER TABLE products DROP COLUMN IF EXISTS description;
