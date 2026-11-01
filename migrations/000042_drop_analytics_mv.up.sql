SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';
DROP MATERIALIZED VIEW IF EXISTS mv_top_products;
DROP MATERIALIZED VIEW IF EXISTS mv_sales_daily;
