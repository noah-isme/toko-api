-- Helper definitions for sqlc generation, parsed AFTER migrations/ (see sqlc.yaml).
--
-- migrations/000049_products_search_trgm_simple.up.sql adds this column inside a DO $$
-- block (so a re-run or the manual large-catalog procedure takes no lock on products).
-- sqlc's parser does not evaluate DO blocks; without this declaration the generated
-- Product model drops SearchVectorSimple and sqlc's schema no longer matches the database.
-- Keep the type in sync with the migration.
ALTER TABLE products ADD COLUMN search_vector_simple tsvector;
