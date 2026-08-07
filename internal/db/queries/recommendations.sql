-- name: UpsertUserProductView :one
INSERT INTO user_product_views (tenant_id, user_id, product_id, view_count, last_viewed_at, updated_at)
VALUES ($1, $2, $3, 1, now(), now())
ON CONFLICT (tenant_id, user_id, product_id)
DO UPDATE SET
    view_count = user_product_views.view_count + 1,
    last_viewed_at = now(),
    updated_at = now()
RETURNING id, tenant_id, user_id, product_id, view_count, last_viewed_at, created_at, updated_at;

-- name: GetUserProductViews :many
SELECT product_id, view_count, last_viewed_at
FROM user_product_views
WHERE tenant_id = $1 AND user_id = $2
ORDER BY last_viewed_at DESC
LIMIT $3;

-- name: GetTopViewedProductsByUser :many
SELECT product_id, view_count
FROM user_product_views
WHERE tenant_id = $1 AND user_id = $2
ORDER BY view_count DESC, last_viewed_at DESC
LIMIT $3;

-- name: UpsertOrderProductPair :one
INSERT INTO order_product_pairs (tenant_id, product_id_a, product_id_b, pair_count, updated_at)
VALUES (
    $1,
    LEAST($2, $3),
    GREATEST($2, $3),
    1,
    now()
)
ON CONFLICT (tenant_id, product_id_a, product_id_b)
DO UPDATE SET
    pair_count = order_product_pairs.pair_count + 1,
    updated_at = now()
RETURNING id, tenant_id, product_id_a, product_id_b, pair_count, created_at, updated_at;

-- name: GetFrequentlyBoughtTogether :many
SELECT 
    opp.product_id_a,
    opp.product_id_b,
    opp.pair_count,
    p.id,
    p.title,
    p.slug,
    p.price,
    p.compare_at,
    p.in_stock,
    p.thumbnail,
    p.badges,
    p.created_at,
    c.id AS category_id,
    c.name AS category_name,
    b.id AS brand_id,
    b.name AS brand_name,
    COALESCE((SELECT AVG(rating) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::float8 AS rating,
    COALESCE((SELECT COUNT(*) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::int AS review_count,
    COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock
FROM order_product_pairs opp
JOIN products p ON (
    (opp.product_id_a = $2 AND p.id = opp.product_id_b) OR
    (opp.product_id_b = $2 AND p.id = opp.product_id_a)
)
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE opp.tenant_id = $1
  AND (opp.product_id_a = $2 OR opp.product_id_b = $2)
  AND p.in_stock = true
ORDER BY opp.pair_count DESC, p.rating DESC
LIMIT $3;

-- name: GetCustomersAlsoViewed :many
SELECT 
    upv.product_id,
    p.id,
    p.title,
    p.slug,
    p.price,
    p.compare_at,
    p.in_stock,
    p.thumbnail,
    p.badges,
    p.created_at,
    c.id AS category_id,
    c.name AS category_name,
    b.id AS brand_id,
    b.name AS brand_name,
    COALESCE((SELECT AVG(rating) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::float8 AS rating,
    COALESCE((SELECT COUNT(*) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::int AS review_count,
    COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock
FROM user_product_views upv
JOIN products p ON p.id = upv.product_id
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE upv.tenant_id = $1
  AND upv.user_id IN (
      SELECT DISTINCT uv2.user_id
      FROM user_product_views uv2
      WHERE uv2.tenant_id = $1 AND uv2.product_id = $2
  )
  AND upv.product_id != $2
  AND p.in_stock = true
GROUP BY upv.product_id, p.id, p.title, p.slug, p.price, p.compare_at, p.in_stock, p.thumbnail, p.badges, p.created_at, c.id, c.name, b.id, b.name
ORDER BY COUNT(DISTINCT upv.user_id) DESC, p.rating DESC
LIMIT $3;

-- name: GetPersonalizedRecommendations :many
WITH user_views AS (
    SELECT upv.product_id, upv.view_count
    FROM user_product_views upv
    WHERE upv.tenant_id = $1 AND upv.user_id = $2
    ORDER BY upv.view_count DESC, upv.last_viewed_at DESC
    LIMIT 50
),
user_categories AS (
    SELECT DISTINCT p.category_id
    FROM user_product_views upv
    JOIN products p ON p.id = upv.product_id
    WHERE upv.tenant_id = $1 AND upv.user_id = $2 AND p.category_id IS NOT NULL
    LIMIT 10
),
user_brands AS (
    SELECT DISTINCT p.brand_id
    FROM user_product_views upv
    JOIN products p ON p.id = upv.product_id
    WHERE upv.tenant_id = $1 AND upv.user_id = $2 AND p.brand_id IS NOT NULL
    LIMIT 10
),
viewed_products AS (
    SELECT upv3.product_id FROM user_product_views upv3 WHERE upv3.tenant_id = $1 AND upv3.user_id = $2
),
category_recs AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.price,
           p.compare_at,
           p.in_stock,
           p.thumbnail,
           p.badges,
           p.created_at,
           c.id AS category_id,
           c.name AS category_name,
           b.id AS brand_id,
           b.name AS brand_name,
           COALESCE((SELECT AVG(rating) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::float8 AS rating,
           COALESCE((SELECT COUNT(*) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::int AS review_count,
           COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock,
           2.0 AS score
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = $1
      AND p.in_stock = true
      AND p.id NOT IN (SELECT product_id FROM viewed_products)
      AND p.category_id IN (SELECT category_id FROM user_categories)
    ORDER BY p.rating DESC, p.created_at DESC
    LIMIT $3
),
brand_recs AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.price,
           p.compare_at,
           p.in_stock,
           p.thumbnail,
           p.badges,
           p.created_at,
           c.id AS category_id,
           c.name AS category_name,
           b.id AS brand_id,
           b.name AS brand_name,
           COALESCE((SELECT AVG(rating) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::float8 AS rating,
           COALESCE((SELECT COUNT(*) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::int AS review_count,
           COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock,
           1.5 AS score
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = $1
      AND p.in_stock = true
      AND p.id NOT IN (SELECT product_id FROM viewed_products)
      AND p.brand_id IN (SELECT brand_id FROM user_brands)
    ORDER BY p.rating DESC, p.created_at DESC
    LIMIT $3
),
trending_recs AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.price,
           p.compare_at,
           p.in_stock,
           p.thumbnail,
           p.badges,
           p.created_at,
           c.id AS category_id,
           c.name AS category_name,
           b.id AS brand_id,
           b.name AS brand_name,
           COALESCE((SELECT AVG(rating) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::float8 AS rating,
           COALESCE((SELECT COUNT(*) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::int AS review_count,
           COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock,
           1.0 AS score
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = $1
      AND p.in_stock = true
      AND p.id NOT IN (SELECT product_id FROM viewed_products)
    ORDER BY p.rating DESC, p.created_at DESC
    LIMIT $3
)
SELECT * FROM (
    SELECT * FROM category_recs
    UNION ALL
    SELECT * FROM brand_recs
    UNION ALL
    SELECT * FROM trending_recs
) combined
ORDER BY score DESC, rating DESC
LIMIT $3;

-- name: UpsertUserRecommendationCache :one
INSERT INTO user_recommendation_cache (tenant_id, user_id, recommendation_type, product_ids, expires_at, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (tenant_id, user_id, recommendation_type)
DO UPDATE SET
    product_ids = EXCLUDED.product_ids,
    expires_at = EXCLUDED.expires_at,
    updated_at = now()
RETURNING id, tenant_id, user_id, recommendation_type, product_ids, expires_at, created_at, updated_at;

-- name: GetUserRecommendationCache :one
SELECT id, tenant_id, user_id, recommendation_type, product_ids, expires_at, created_at, updated_at
FROM user_recommendation_cache
WHERE tenant_id = $1 AND user_id = $2 AND recommendation_type = $3 AND expires_at > now();

-- name: DeleteExpiredRecommendationCache :exec
DELETE FROM user_recommendation_cache WHERE expires_at <= now();

-- name: GetTrendingProducts :many
SELECT p.id,
       p.title,
       p.slug,
       p.price,
       p.compare_at,
       p.in_stock,
       p.thumbnail,
       p.badges,
       p.created_at,
       c.id AS category_id,
       c.name AS category_name,
       b.id AS brand_id,
       b.name AS brand_name,
       COALESCE((SELECT AVG(rating) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::float8 AS rating,
       COALESCE((SELECT COUNT(*) FROM reviews WHERE product_id = p.id AND tenant_id = p.tenant_id), 0)::int AS review_count,
       COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE p.tenant_id = $1
  AND p.in_stock = true
ORDER BY p.rating DESC, p.created_at DESC
LIMIT $2;