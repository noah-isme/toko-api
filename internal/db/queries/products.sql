-- name: CountProductsPublic :one
SELECT COUNT(*)
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN categories c ON c.id = p.category_id
WHERE (sqlc.narg(q) IS NULL OR p.title ILIKE '%%' || sqlc.arg(q) || '%%')
  AND (sqlc.narg(category_slug) IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(brand_slug) IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(min_price) IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price) IS NULL OR p.price <= sqlc.arg(max_price))
  AND (sqlc.narg(in_stock) IS NULL OR p.in_stock = sqlc.arg(in_stock));

-- name: ListProductsPublic :many
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
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN categories c ON c.id = p.category_id
WHERE (sqlc.narg(q) IS NULL OR p.title ILIKE '%%' || sqlc.arg(q) || '%%')
  AND (sqlc.narg(category_slug) IS NULL OR c.slug = sqlc.arg(category_slug))
  AND (sqlc.narg(brand_slug) IS NULL OR b.slug = sqlc.arg(brand_slug))
  AND (sqlc.narg(min_price) IS NULL OR p.price >= sqlc.arg(min_price))
  AND (sqlc.narg(max_price) IS NULL OR p.price <= sqlc.arg(max_price))
  AND (sqlc.narg(in_stock) IS NULL OR p.in_stock = sqlc.arg(in_stock))
ORDER BY CASE WHEN sqlc.arg(sort) = 'price:asc' THEN p.price END ASC,
         CASE WHEN sqlc.arg(sort) = 'price:desc' THEN p.price END DESC,
         CASE WHEN sqlc.arg(sort) = 'title:asc' THEN p.title END ASC,
         CASE WHEN sqlc.arg(sort) = 'title:desc' THEN p.title END DESC,
         p.created_at DESC
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: GetProductBySlug :one
SELECT id,
       title,
       slug,
       price,
       compare_at,
       in_stock,
       thumbnail,
       badges,
       brand_id,
       category_id,
       created_at
FROM products
WHERE slug = $1
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
ORDER BY p.created_at DESC
LIMIT 8;

-- name: GetProductForCart :one
SELECT id,
       title,
       slug,
       price,
       category_id,
       brand_id
FROM products
WHERE id = $1
LIMIT 1;

-- name: GetVariantForCart :one
SELECT id,
       product_id,
       price,
       stock
FROM product_variants
WHERE id = $1
LIMIT 1;
