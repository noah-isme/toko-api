-- Admin catalog management queries. These power the /api/v1/admin/{products,
-- categories,brands} endpoints and are intentionally separate from the public
-- catalog queries, which are cached and shaped for storefront consumption.

-- name: AdminCountProducts :one
SELECT COUNT(*)
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN categories c ON c.id = p.category_id
WHERE (sqlc.narg(search)::text IS NULL
       OR p.title ILIKE '%' || sqlc.arg(search)::text || '%'
       OR p.slug ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug)::text)
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug)::text)
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock)::boolean);

-- name: AdminListProducts :many
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
       (SELECT sku FROM product_variants WHERE product_id = p.id ORDER BY sku NULLS LAST LIMIT 1) AS primary_sku
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN categories c ON c.id = p.category_id
WHERE (sqlc.narg(search)::text IS NULL
       OR p.title ILIKE '%' || sqlc.arg(search)::text || '%'
       OR p.slug ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(category_slug)::text IS NULL OR c.slug = sqlc.arg(category_slug)::text)
  AND (sqlc.narg(brand_slug)::text IS NULL OR b.slug = sqlc.arg(brand_slug)::text)
  AND (sqlc.narg(in_stock)::boolean IS NULL OR p.in_stock = sqlc.arg(in_stock)::boolean)
ORDER BY p.created_at DESC, p.id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

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
       COALESCE((SELECT SUM(stock) FROM product_variants WHERE product_id = p.id), 0)::int AS total_stock
FROM products p
LEFT JOIN brands b ON b.id = p.brand_id
LEFT JOIN categories c ON c.id = p.category_id
WHERE p.id = sqlc.arg(id)
LIMIT 1;

-- name: AdminCreateProduct :one
INSERT INTO products (title, slug, brand_id, category_id, price, compare_at, in_stock, thumbnail, badges, description, tenant_id)
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
    COALESCE(sqlc.narg(tenant_id)::uuid, (SELECT id FROM tenants WHERE slug = 'default'))
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
    updated_at  = now()
WHERE id = sqlc.arg(id)
RETURNING id;

-- name: AdminDeleteProduct :execrows
DELETE FROM products WHERE id = sqlc.arg(id);

-- name: AdminGetProductIDBySlug :one
SELECT id FROM products WHERE slug = sqlc.arg(slug) LIMIT 1;

-- name: AdminReplaceProductImages :exec
DELETE FROM product_images WHERE product_id = sqlc.arg(product_id);

-- name: AdminInsertProductImage :exec
INSERT INTO product_images (product_id, url, sort_order)
VALUES (sqlc.arg(product_id), sqlc.arg(url), sqlc.arg(sort_order));

-- name: AdminDeleteProductSpecs :exec
DELETE FROM product_specs WHERE product_id = sqlc.arg(product_id);

-- name: AdminInsertProductSpec :exec
INSERT INTO product_specs (product_id, key, value)
VALUES (sqlc.arg(product_id), sqlc.arg(key), sqlc.arg(value));

-- name: AdminInsertProductVariant :one
INSERT INTO product_variants (product_id, sku, price, stock, attributes)
VALUES (sqlc.arg(product_id), sqlc.narg(sku), sqlc.arg(price), sqlc.arg(stock), sqlc.arg(attributes))
RETURNING id;

-- name: AdminUpdateProductVariant :one
UPDATE product_variants
SET sku        = COALESCE(sqlc.narg(sku), sku),
    price      = COALESCE(sqlc.narg(price), price),
    stock      = COALESCE(sqlc.narg(stock), stock),
    attributes = COALESCE(sqlc.narg(attributes), attributes)
WHERE id = sqlc.arg(id) AND product_id = sqlc.arg(product_id)
RETURNING id, product_id, sku, price, stock, attributes;

-- name: AdminDeleteProductVariant :execrows
DELETE FROM product_variants WHERE id = sqlc.arg(id) AND product_id = sqlc.arg(product_id);

-- name: AdminGetPrimaryVariant :one
SELECT id, product_id, sku, price, stock, attributes
FROM product_variants
WHERE product_id = sqlc.arg(product_id)
ORDER BY sku NULLS LAST, id
LIMIT 1;

-- name: AdminSetProductStockFlag :exec
UPDATE products
SET in_stock   = sqlc.arg(in_stock),
    updated_at = now()
WHERE id = sqlc.arg(id);

-- name: AdminListCategories :many
SELECT c.id,
       c.name,
       c.slug,
       c.parent_id,
       c.created_at,
       c.updated_at,
       COALESCE((SELECT COUNT(*) FROM products WHERE category_id = c.id), 0)::int AS product_count
FROM categories c
ORDER BY c.name ASC;

-- name: AdminCreateCategory :one
INSERT INTO categories (name, slug, parent_id, tenant_id)
VALUES (
    sqlc.arg(name),
    sqlc.arg(slug),
    sqlc.narg(parent_id),
    COALESCE(sqlc.narg(tenant_id)::uuid, (SELECT id FROM tenants WHERE slug = 'default'))
)
RETURNING id, name, slug, parent_id, created_at, updated_at;

-- name: AdminUpdateCategory :one
UPDATE categories
SET name       = COALESCE(sqlc.narg(name), name),
    slug       = COALESCE(sqlc.narg(slug), slug),
    parent_id  = CASE WHEN sqlc.arg(set_parent)::boolean THEN sqlc.narg(parent_id) ELSE parent_id END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id, name, slug, parent_id, created_at, updated_at;

-- name: AdminDeleteCategory :execrows
DELETE FROM categories WHERE id = sqlc.arg(id);

-- name: AdminListBrands :many
SELECT b.id,
       b.name,
       b.slug,
       b.created_at,
       b.updated_at,
       COALESCE((SELECT COUNT(*) FROM products WHERE brand_id = b.id), 0)::int AS product_count
FROM brands b
ORDER BY b.name ASC;

-- name: AdminCreateBrand :one
INSERT INTO brands (name, slug, tenant_id)
VALUES (
    sqlc.arg(name),
    sqlc.arg(slug),
    COALESCE(sqlc.narg(tenant_id)::uuid, (SELECT id FROM tenants WHERE slug = 'default'))
)
RETURNING id, name, slug, created_at, updated_at;

-- name: AdminUpdateBrand :one
UPDATE brands
SET name       = COALESCE(sqlc.narg(name), name),
    slug       = COALESCE(sqlc.narg(slug), slug),
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING id, name, slug, created_at, updated_at;

-- name: AdminDeleteBrand :execrows
DELETE FROM brands WHERE id = sqlc.arg(id);
