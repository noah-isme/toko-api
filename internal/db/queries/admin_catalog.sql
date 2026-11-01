-- Admin catalog management queries. These power the /api/v1/admin/{products,
-- categories,brands} endpoints and are intentionally separate from the public
-- catalog queries, which are cached and shaped for storefront consumption.

-- The admin product list, its count and the products export share one shape:
-- `base` applies the tenant and column filters, `classified` adds the
-- product-level stock status (mirrors catalog.StockStatus in
-- internal/catalog/lowstock.go: no variants -> in_stock flag; total 0 -> out;
-- total <= effective threshold -> low; else in). Keep the three copies in sync.

-- name: AdminCountProducts :one
WITH base AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.status,
           p.price,
           p.compare_at,
           p.in_stock,
           p.low_stock_threshold,
           p.thumbnail,
           p.badges,
           p.description,
           p.created_at,
           p.updated_at,
           c.id   AS category_id,
           c.name AS category_name,
           c.slug AS category_slug,
           b.id   AS brand_id,
           b.name AS brand_name,
           b.slug AS brand_slug,
           COALESCE((SELECT SUM(v.stock) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS total_stock,
           COALESCE((SELECT COUNT(*) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS variant_count,
           (SELECT v.sku FROM product_variants v WHERE v.product_id = p.id ORDER BY v.sku NULLS LAST LIMIT 1) AS primary_sku
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = sqlc.arg(tenant_id)
      AND (sqlc.narg(search)::text IS NULL
           OR p.title ILIKE '%' || sqlc.narg(search)::text || '%'
           OR p.slug ILIKE '%' || sqlc.narg(search)::text || '%')
      AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.narg(category_slug)::text)
      AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.narg(brand_slug)::text)
      AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.narg(in_stock)::boolean)
      AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
),
classified AS (
    SELECT base.*,
           (CASE
                WHEN base.variant_count = 0 THEN CASE WHEN base.in_stock THEN 'in' ELSE 'out' END
                WHEN base.total_stock <= 0 THEN 'out'
                WHEN base.total_stock <= COALESCE(base.low_stock_threshold, sqlc.arg(default_threshold)::int) THEN 'low'
                ELSE 'in'
            END)::text AS stock_status
    FROM base
)
SELECT COUNT(*)
FROM classified
WHERE (sqlc.narg(stock_status)::text IS NULL OR classified.stock_status = sqlc.narg(stock_status)::text);

-- name: AdminListProducts :many
WITH base AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.status,
           p.price,
           p.compare_at,
           p.in_stock,
           p.low_stock_threshold,
           p.thumbnail,
           p.badges,
           p.description,
           p.created_at,
           p.updated_at,
           c.id   AS category_id,
           c.name AS category_name,
           c.slug AS category_slug,
           b.id   AS brand_id,
           b.name AS brand_name,
           b.slug AS brand_slug,
           COALESCE((SELECT SUM(v.stock) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS total_stock,
           COALESCE((SELECT COUNT(*) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS variant_count,
           (SELECT v.sku FROM product_variants v WHERE v.product_id = p.id ORDER BY v.sku NULLS LAST LIMIT 1) AS primary_sku
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = sqlc.arg(tenant_id)
      AND (sqlc.narg(search)::text IS NULL
           OR p.title ILIKE '%' || sqlc.narg(search)::text || '%'
           OR p.slug ILIKE '%' || sqlc.narg(search)::text || '%')
      AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.narg(category_slug)::text)
      AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.narg(brand_slug)::text)
      AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.narg(in_stock)::boolean)
      AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
),
classified AS (
    SELECT base.*,
           (CASE
                WHEN base.variant_count = 0 THEN CASE WHEN base.in_stock THEN 'in' ELSE 'out' END
                WHEN base.total_stock <= 0 THEN 'out'
                WHEN base.total_stock <= COALESCE(base.low_stock_threshold, sqlc.arg(default_threshold)::int) THEN 'low'
                ELSE 'in'
            END)::text AS stock_status
    FROM base
)
SELECT id, title, slug, status, price, compare_at, in_stock, low_stock_threshold, thumbnail, badges,
       description, created_at, updated_at, category_id, category_name, brand_id, brand_name,
       total_stock, variant_count, primary_sku, stock_status
FROM classified
WHERE (sqlc.narg(stock_status)::text IS NULL OR classified.stock_status = sqlc.narg(stock_status)::text)
ORDER BY CASE WHEN sqlc.arg(sort)::text = 'name:asc' THEN classified.title END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'name:desc' THEN classified.title END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'price:asc' THEN classified.price END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'price:desc' THEN classified.price END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'stock:asc' THEN classified.total_stock END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'stock:desc' THEN classified.total_stock END DESC,
         CASE WHEN sqlc.arg(sort)::text = 'created:asc' THEN classified.created_at END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'updated:asc' THEN classified.updated_at END ASC,
         CASE WHEN sqlc.arg(sort)::text = 'updated:desc' THEN classified.updated_at END DESC,
         classified.created_at DESC,
         classified.id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: AdminExportProductPage :many
-- One page of products (keyset on created_at DESC, id DESC) with their variants
-- LEFT JOINed, so a product's variants never straddle a page boundary.
WITH base AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.status,
           p.price,
           p.compare_at,
           p.in_stock,
           p.low_stock_threshold,
           p.thumbnail,
           p.badges,
           p.description,
           p.created_at,
           p.updated_at,
           c.id   AS category_id,
           c.name AS category_name,
           c.slug AS category_slug,
           b.id   AS brand_id,
           b.name AS brand_name,
           b.slug AS brand_slug,
           COALESCE((SELECT SUM(v.stock) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS total_stock,
           COALESCE((SELECT COUNT(*) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS variant_count,
           (SELECT v.sku FROM product_variants v WHERE v.product_id = p.id ORDER BY v.sku NULLS LAST LIMIT 1) AS primary_sku
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = sqlc.arg(tenant_id)
      AND (sqlc.narg(search)::text IS NULL
           OR p.title ILIKE '%' || sqlc.narg(search)::text || '%'
           OR p.slug ILIKE '%' || sqlc.narg(search)::text || '%')
      AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.narg(category_slug)::text)
      AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.narg(brand_slug)::text)
      AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.narg(in_stock)::boolean)
      AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
),
classified AS (
    SELECT base.*,
           (CASE
                WHEN base.variant_count = 0 THEN CASE WHEN base.in_stock THEN 'in' ELSE 'out' END
                WHEN base.total_stock <= 0 THEN 'out'
                WHEN base.total_stock <= COALESCE(base.low_stock_threshold, sqlc.arg(default_threshold)::int) THEN 'low'
                ELSE 'in'
            END)::text AS stock_status
    FROM base
),
page AS (
    SELECT classified.*
    FROM classified
    WHERE (sqlc.narg(stock_status)::text IS NULL OR classified.stock_status = sqlc.narg(stock_status)::text)
      AND (sqlc.narg(after_created_at)::timestamptz IS NULL
           OR (classified.created_at, classified.id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
    ORDER BY classified.created_at DESC, classified.id DESC
    LIMIT sqlc.arg(batch_size)::int
)
SELECT pg.id AS product_id,
       pg.title,
       pg.slug,
       pg.status,
       pg.price,
       pg.compare_at,
       pg.low_stock_threshold,
       pg.in_stock,
       pg.description,
       pg.created_at,
       pg.updated_at,
       pg.category_slug,
       pg.brand_slug,
       pg.stock_status,
       v.id AS variant_id,
       v.sku,
       v.price AS variant_price,
       v.stock,
       v.attributes
FROM page pg
LEFT JOIN product_variants v ON v.product_id = pg.id
ORDER BY pg.created_at DESC, pg.id DESC, v.id;

-- name: AdminStockStatusCounts :one
WITH base AS (
    SELECT p.id,
           p.title,
           p.slug,
           p.status,
           p.price,
           p.compare_at,
           p.in_stock,
           p.low_stock_threshold,
           p.thumbnail,
           p.badges,
           p.description,
           p.created_at,
           p.updated_at,
           c.id   AS category_id,
           c.name AS category_name,
           c.slug AS category_slug,
           b.id   AS brand_id,
           b.name AS brand_name,
           b.slug AS brand_slug,
           COALESCE((SELECT SUM(v.stock) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS total_stock,
           COALESCE((SELECT COUNT(*) FROM product_variants v WHERE v.product_id = p.id), 0)::int AS variant_count,
           (SELECT v.sku FROM product_variants v WHERE v.product_id = p.id ORDER BY v.sku NULLS LAST LIMIT 1) AS primary_sku
    FROM products p
    LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
    WHERE p.tenant_id = sqlc.arg(tenant_id)
      AND (sqlc.narg(search)::text IS NULL
           OR p.title ILIKE '%' || sqlc.narg(search)::text || '%'
           OR p.slug ILIKE '%' || sqlc.narg(search)::text || '%')
      AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.narg(category_slug)::text)
      AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.narg(brand_slug)::text)
      AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.narg(in_stock)::boolean)
      AND (sqlc.narg(status)::text IS NULL OR p.status = sqlc.narg(status)::text)
),
classified AS (
    SELECT base.*,
           (CASE
                WHEN base.variant_count = 0 THEN CASE WHEN base.in_stock THEN 'in' ELSE 'out' END
                WHEN base.total_stock <= 0 THEN 'out'
                WHEN base.total_stock <= COALESCE(base.low_stock_threshold, sqlc.arg(default_threshold)::int) THEN 'low'
                ELSE 'in'
            END)::text AS stock_status
    FROM base
)
SELECT COUNT(*) FILTER (WHERE classified.stock_status = 'low')::bigint AS low_stock,
       COUNT(*) FILTER (WHERE classified.stock_status = 'out')::bigint AS out_of_stock
FROM classified;

-- name: AdminGetProduct :one
SELECT p.id,
       p.title,
       p.slug,
       p.price,
       p.compare_at,
       p.in_stock,
       p.thumbnail,
       p.badges,
       p.description,
       p.created_at,
       p.updated_at,
       c.id   AS category_id,
       c.name AS category_name,
       b.id   AS brand_id,
       b.name AS brand_name,
       COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock,
       COALESCE((SELECT COUNT(*) FROM product_variants WHERE product_id = p.id), 0)::int AS variant_count,
       p.status,
       p.low_stock_threshold,
       c.slug AS category_slug,
       b.slug AS brand_slug
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id AND b.tenant_id = p.tenant_id
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = p.tenant_id
WHERE p.id = sqlc.arg(id)
  AND p.tenant_id = sqlc.arg(tenant_id)
LIMIT 1;

-- name: AdminCreateProduct :one
INSERT INTO products (title, slug, brand_id, category_id, price, compare_at, in_stock, thumbnail, badges, description, tenant_id, status, low_stock_threshold)
VALUES (
    sqlc.arg(title),
    sqlc.arg(slug),
    sqlc.narg(brand_id),
    sqlc.narg(category_id),
    sqlc.arg(price),
    sqlc.narg(compare_at),
    sqlc.arg(in_stock),
    sqlc.narg(thumbnail),
    sqlc.arg(badges),
    sqlc.narg(description),
    sqlc.arg(tenant_id),
    sqlc.arg(status),
    sqlc.narg(low_stock_threshold)
)
RETURNING id;

-- name: AdminUpdateProduct :one
UPDATE products
SET title       = COALESCE(sqlc.narg(title), title),
    slug        = COALESCE(sqlc.narg(slug), slug),
    brand_id    = CASE WHEN sqlc.arg(set_brand)::boolean THEN sqlc.narg(brand_id) ELSE brand_id END,
    category_id = CASE WHEN sqlc.arg(set_category)::boolean THEN sqlc.narg(category_id) ELSE category_id END,
    price       = COALESCE(sqlc.narg(price), price),
    compare_at  = CASE WHEN sqlc.arg(set_compare_at)::boolean THEN sqlc.narg(compare_at) ELSE compare_at END,
    in_stock    = COALESCE(sqlc.narg(in_stock), in_stock),
    thumbnail   = CASE WHEN sqlc.arg(set_thumbnail)::boolean THEN sqlc.narg(thumbnail) ELSE thumbnail END,
    badges      = CASE WHEN sqlc.arg(set_badges)::boolean THEN sqlc.arg(badges)::text[] ELSE badges END,
    description = CASE WHEN sqlc.arg(set_description)::boolean THEN sqlc.narg(description) ELSE description END,
    status      = COALESCE(sqlc.narg(status), status),
    low_stock_threshold = CASE WHEN sqlc.arg(set_low_stock_threshold)::boolean THEN sqlc.narg(low_stock_threshold) ELSE low_stock_threshold END,
    updated_at  = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
RETURNING id;

-- name: AdminDeleteProduct :execrows
DELETE FROM products WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id);

-- name: AdminGetProductIDBySlug :one
SELECT id FROM products WHERE slug = sqlc.arg(slug) AND tenant_id = sqlc.arg(tenant_id) LIMIT 1;

-- name: AdminReplaceProductImages :exec
DELETE FROM product_images WHERE product_id = sqlc.arg(product_id)
  AND EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id));

-- name: AdminInsertProductImage :exec
INSERT INTO product_images (product_id, url, sort_order)
SELECT sqlc.arg(product_id), sqlc.arg(url), sqlc.arg(sort_order)
WHERE EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id));

-- name: AdminDeleteProductSpecs :exec
DELETE FROM product_specs WHERE product_id = sqlc.arg(product_id)
  AND EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id));

-- name: AdminInsertProductSpec :exec
INSERT INTO product_specs (product_id, key, value)
SELECT sqlc.arg(product_id), sqlc.arg(key), sqlc.arg(value)
WHERE EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id));

-- name: AdminInsertProductVariant :one
INSERT INTO product_variants (product_id, sku, price, stock, attributes)
SELECT sqlc.arg(product_id), sqlc.narg(sku), sqlc.arg(price), sqlc.arg(stock), sqlc.arg(attributes)
WHERE EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id))
RETURNING id;

-- name: AdminUpdateProductVariant :one
UPDATE product_variants
SET sku        = COALESCE(sqlc.narg(sku), sku),
    price      = COALESCE(sqlc.narg(price), price),
    stock      = COALESCE(sqlc.narg(stock), stock),
    attributes = COALESCE(sqlc.narg(attributes), attributes)
WHERE product_variants.id = sqlc.arg(id) AND product_id = sqlc.arg(product_id)
  AND EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id))
RETURNING id, product_id, sku, price, stock, attributes;

-- name: AdminDeleteProductVariant :execrows
DELETE FROM product_variants
WHERE product_variants.id = sqlc.arg(id) AND product_id = sqlc.arg(product_id)
  AND EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id));

-- name: AdminGetPrimaryVariant :one
SELECT id, product_id, sku, price, stock, attributes
FROM product_variants
WHERE product_id = sqlc.arg(product_id)
  AND EXISTS (SELECT 1 FROM products WHERE id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id))
ORDER BY sku NULLS LAST, id
LIMIT 1;

-- name: AdminSetProductStockFlag :exec
UPDATE products
SET in_stock   = sqlc.arg(in_stock),
    updated_at = now()
WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id);

-- name: AdminListCategories :many
SELECT c.id,
       c.name,
       c.slug,
       c.parent_id,
       c.created_at,
       c.updated_at,
       COALESCE((SELECT COUNT(*) FROM products WHERE category_id = c.id AND tenant_id = c.tenant_id), 0)::int AS product_count
FROM categories c
WHERE c.tenant_id = sqlc.arg(tenant_id)
ORDER BY c.name ASC;

-- name: AdminCreateCategory :one
INSERT INTO categories (name, slug, parent_id, tenant_id)
VALUES (
    sqlc.arg(name),
    sqlc.arg(slug),
    sqlc.narg(parent_id),
    sqlc.arg(tenant_id)
)
RETURNING id, name, slug, parent_id, created_at, updated_at;

-- name: AdminUpdateCategory :one
UPDATE categories
SET name       = COALESCE(sqlc.narg(name), name),
    slug       = COALESCE(sqlc.narg(slug), slug),
    parent_id  = CASE WHEN sqlc.arg(set_parent)::boolean THEN sqlc.narg(parent_id) ELSE parent_id END,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
RETURNING id, name, slug, parent_id, created_at, updated_at;

-- name: AdminDeleteCategory :execrows
DELETE FROM categories WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id);

-- name: AdminListBrands :many
SELECT b.id,
       b.name,
       b.slug,
       b.created_at,
       b.updated_at,
       COALESCE((SELECT COUNT(*) FROM products WHERE brand_id = b.id AND tenant_id = b.tenant_id), 0)::int AS product_count
FROM brands b
WHERE b.tenant_id = sqlc.arg(tenant_id)
ORDER BY b.name ASC;

-- name: AdminCreateBrand :one
INSERT INTO brands (name, slug, tenant_id)
VALUES (
    sqlc.arg(name),
    sqlc.arg(slug),
    sqlc.arg(tenant_id)
)
RETURNING id, name, slug, created_at, updated_at;

-- name: AdminUpdateBrand :one
UPDATE brands
SET name       = COALESCE(sqlc.narg(name), name),
    slug       = COALESCE(sqlc.narg(slug), slug),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
RETURNING id, name, slug, created_at, updated_at;

-- name: AdminDeleteBrand :execrows
DELETE FROM brands WHERE id = sqlc.arg(id) AND tenant_id = sqlc.arg(tenant_id);
