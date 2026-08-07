-- Down migration for recommendation engine tracking tables

DROP INDEX IF EXISTS idx_user_recommendation_cache_expires;
DROP INDEX IF EXISTS idx_user_recommendation_cache_unique;
DROP TABLE IF EXISTS user_recommendation_cache;

DROP INDEX IF EXISTS idx_order_product_pairs_count;
DROP INDEX IF EXISTS idx_order_product_pairs_b;
DROP INDEX IF EXISTS idx_order_product_pairs_a;
DROP INDEX IF EXISTS idx_order_product_pairs_unique;
DROP TABLE IF EXISTS order_product_pairs;

DROP INDEX IF EXISTS idx_user_product_views_last_viewed;
DROP INDEX IF EXISTS idx_user_product_views_product;
DROP INDEX IF EXISTS idx_user_product_views_user;
DROP INDEX IF EXISTS idx_user_product_views_unique;
DROP TABLE IF EXISTS user_product_views;