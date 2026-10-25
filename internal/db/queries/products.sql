-- Search predicate shared by every public list/facet query below:
--   match_mode 'fts'   -> websearch_to_tsquery('simple', q) against the 'simple' tsvector
--                         expression, served by idx_products_search_simple (migration 000049).
--                         The expression must stay character-for-character identical to the
--                         index definition or the planner falls back to a sequential scan.
--   match_mode 'fuzzy' -> pg_trgm word similarity (q <% title), served by idx_products_title_trgm;
--                         the caller sets pg_trgm.word_similarity_threshold with SET LOCAL.
-- Cross-stream note: these are public storefront reads. When products.status (catalog-ops,
-- migration 000053) exists, every query in this block plus SuggestProductTitles must also
-- filter p.status = 'published'.

-- name: CountProductsPublic :one
SELECT COUNT(*)
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(q)::text IS NULL
       OR (sqlc.arg(match_mode)::text = 'fuzzy' AND sqlc.arg(q)::text <% p.title)
       OR (sqlc.arg(match_mode)::text <> 'fuzzy'
           AND (setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B'))
               @@ websearch_to_tsquery('simple', sqlc.arg(q))))
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(min_price)::bigint IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price <= sqlc.arg(max_price))
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock));

-- name: ListProductsPublic :many
SELECT p.id,
       p.title,
       p.slug,
       p.price,
       p.compare_at,
       p.in_stock,
       p.thumbnail,
       p.badges,
       p.created_at,
       c.id   AS category_id,
       c.name AS category_name,
       b.id   AS brand_id,
       b.name AS brand_name,
       COALESCE(rv.avg_rating, 0)::float8 AS rating,
       COALESCE(rv.review_count, 0)::int AS review_count,
       COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
LEFT JOIN LATERAL (
    SELECT AVG(r.rating)::float8 AS avg_rating, COUNT(*)::int AS review_count
    FROM reviews r
    WHERE r.product_id = p.id AND r.tenant_id = p.tenant_id
) rv ON true
LEFT JOIN product_sales ps ON ps.product_id = p.id AND ps.tenant_id = p.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(q)::text IS NULL
       OR (sqlc.arg(match_mode)::text = 'fuzzy' AND sqlc.arg(q)::text <% p.title)
       OR (sqlc.arg(match_mode)::text <> 'fuzzy'
           AND (setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B'))
               @@ websearch_to_tsquery('simple', sqlc.arg(q))))
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(min_price)::bigint IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price <= sqlc.arg(max_price))
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock))
ORDER BY CASE WHEN sqlc.arg(sort)::text = 'price:asc' THEN p.price END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'price:desc' THEN p.price END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'title:asc' THEN p.title END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'title:desc' THEN p.title END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'popularity:desc' THEN COALESCE(ps.units_sold, 0) END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'rating:desc' THEN COALESCE(rv.avg_rating, 0) END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'rating:desc' THEN COALESCE(rv.review_count, 0) END DESC,
         CASE
           WHEN sqlc.narg(q)::text IS NULL THEN NULL
           WHEN sqlc.arg(match_mode)::text = 'fuzzy' THEN word_similarity(sqlc.arg(q)::text, p.title)::float8
           ELSE ts_rank((setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B')), websearch_to_tsquery('simple', sqlc.arg(q)))::float8
         END DESC,
         p.created_at DESC,
         p.id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- Facets: each query applies the search predicate and every filter except its own dimension.

-- name: FacetCategoriesPublic :many
SELECT c.id, c.name, c.slug, COUNT(*)::bigint AS count
FROM products p
JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(q)::text IS NULL
       OR (sqlc.arg(match_mode)::text = 'fuzzy' AND sqlc.arg(q)::text <% p.title)
       OR (sqlc.arg(match_mode)::text <> 'fuzzy'
           AND (setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B'))
               @@ websearch_to_tsquery('simple', sqlc.arg(q))))
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(min_price)::bigint IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price <= sqlc.arg(max_price))
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock))
GROUP BY c.id, c.name, c.slug
ORDER BY count DESC, c.name ASC
LIMIT 50;

-- name: FacetBrandsPublic :many
SELECT b.id, b.name, b.slug, COUNT(*)::bigint AS count
FROM products p
JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(q)::text IS NULL
       OR (sqlc.arg(match_mode)::text = 'fuzzy' AND sqlc.arg(q)::text <% p.title)
       OR (sqlc.arg(match_mode)::text <> 'fuzzy'
           AND (setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B'))
               @@ websearch_to_tsquery('simple', sqlc.arg(q))))
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(min_price)::bigint IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price <= sqlc.arg(max_price))
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock))
GROUP BY b.id, b.name, b.slug
ORDER BY count DESC, b.name ASC
LIMIT 50;

-- bucket = number of edges <= price, i.e. 0 for price < edges[1], len(edges) for price >= last edge.
-- name: FacetPriceBucketsPublic :many
SELECT (SELECT COUNT(*) FROM unnest(sqlc.arg(edges)::bigint[]) AS e(edge) WHERE e.edge <= p.price)::int AS bucket,
       COUNT(*)::bigint AS count
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(q)::text IS NULL
       OR (sqlc.arg(match_mode)::text = 'fuzzy' AND sqlc.arg(q)::text <% p.title)
       OR (sqlc.arg(match_mode)::text <> 'fuzzy'
           AND (setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B'))
               @@ websearch_to_tsquery('simple', sqlc.arg(q))))
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock))
GROUP BY 1
ORDER BY 1;

-- name: FacetInStockPublic :many
SELECT p.in_stock, COUNT(*)::bigint AS count
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(q)::text IS NULL
       OR (sqlc.arg(match_mode)::text = 'fuzzy' AND sqlc.arg(q)::text <% p.title)
       OR (sqlc.arg(match_mode)::text <> 'fuzzy'
           AND (setweight(to_tsvector('simple', coalesce(p.title, '')), 'A') ||
                setweight(to_tsvector('simple', coalesce(p.description, '')), 'B'))
               @@ websearch_to_tsquery('simple', sqlc.arg(q))))
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(min_price)::bigint IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price <= sqlc.arg(max_price))
GROUP BY p.in_stock;

-- Cold path ("did you mean"): only runs after both FTS and fuzzy found nothing, so a scan of
-- the tenant's titles with the function form of word_similarity is acceptable.
-- name: SuggestProductTitles :many
SELECT p.title, p.slug, word_similarity(sqlc.arg(q)::text, p.title)::float8 AS score
FROM products p
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND word_similarity(sqlc.arg(q)::text, p.title) >= sqlc.arg(min_score)::float4
ORDER BY score DESC, p.title ASC
LIMIT sqlc.arg(limit_value);

-- name: GetProductBySlug :one
SELECT id,
       title,
       slug,
       description,
       price,
       compare_at,
       in_stock,
       thumbnail,
       badges,
       brand_id,
       category_id,
       created_at,
       COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = products.id), 0)::int AS total_stock
FROM products
WHERE slug = sqlc.arg(slug)
  AND tenant_id = sqlc.arg(tenant_id)
LIMIT 1;

-- name: ListVariantsByProduct :many
SELECT id,
       product_id,
       sku,
       price,
       stock,
       attributes
FROM product_variants
WHERE product_id = $1
ORDER BY sku NULLS LAST, id;

-- name: ListImagesByProduct :many
SELECT id,
       product_id,
       url,
       sort_order
FROM product_images
WHERE product_id = $1
ORDER BY sort_order ASC, id;

-- name: ListSpecsByProduct :many
SELECT id,
       product_id,
       key,
       value
FROM product_specs
WHERE product_id = $1
ORDER BY key ASC, id;

-- name: ListRelatedByCategory :many
SELECT p.id,
       p.title,
       p.slug,
       p.price,
       p.compare_at,
       p.in_stock,
       p.thumbnail,
       p.badges,
       p.created_at
FROM products p
WHERE p.category_id = $1
  AND p.slug <> $2
  AND p.tenant_id = sqlc.arg(tenant_id)
ORDER BY p.created_at DESC
LIMIT 8;

-- name: GetProductForCart :one
SELECT id,
       title,
       slug,
       price,
       category_id,
       brand_id,
       in_stock
FROM products
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
LIMIT 1;

-- name: GetVariantForCart :one
SELECT v.id,
       v.product_id,
       v.price,
       v.stock
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE v.id = sqlc.arg(id)
  AND p.tenant_id = sqlc.arg(tenant_id)
LIMIT 1;
