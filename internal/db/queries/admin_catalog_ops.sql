-- Admin catalog operations: bulk updates, CSV import lookups, CSV export and
-- low-stock settings. product_variants has no tenant_id, so every variant query
-- scopes through products.tenant_id.

-- name: AdminGetProductsByIDsForUpdate :many
-- Locks the target products in id order so concurrent bulk requests cannot deadlock.
SELECT id, slug, title, price, compare_at, status, low_stock_threshold, description, category_id, brand_id
FROM products
WHERE id = ANY(sqlc.arg(ids)::uuid[])
  AND tenant_id = sqlc.arg(tenant_id)
ORDER BY id
FOR UPDATE;

-- name: AdminLockVariantsByIDs :many
SELECT v.id, v.product_id, v.sku, v.price, v.stock, v.attributes, p.slug
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE v.id = ANY(sqlc.arg(ids)::uuid[])
  AND p.tenant_id = sqlc.arg(tenant_id)
ORDER BY v.id
FOR UPDATE OF v;

-- name: AdminLockVariantsByIDsOrProducts :many
-- Locks the listed variants plus every variant of the listed products, in one
-- statement and in id order (bulk price with applyTo=both|variants).
SELECT v.id, v.product_id, v.sku, v.price, v.stock, v.attributes, p.slug
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE (v.id = ANY(sqlc.arg(ids)::uuid[]) OR v.product_id = ANY(sqlc.arg(product_ids)::uuid[]))
  AND p.tenant_id = sqlc.arg(tenant_id)
ORDER BY v.id
FOR UPDATE OF v;

-- name: AdminBulkUpdateProductPrice :one
UPDATE products
SET price      = sqlc.arg(price),
    compare_at = CASE WHEN sqlc.arg(set_compare_at)::boolean THEN sqlc.narg(compare_at) ELSE compare_at END,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
RETURNING id, slug, price, compare_at;

-- name: AdminSetVariantPrice :one
UPDATE product_variants v
SET price = sqlc.arg(price)
FROM products p
WHERE v.id = sqlc.arg(id)
  AND p.id = v.product_id
  AND p.tenant_id = sqlc.arg(tenant_id)
RETURNING v.id, v.product_id, v.sku, v.price;

-- name: AdminBulkSetProductStatus :one
UPDATE products
SET status     = sqlc.arg(status),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
RETURNING id, slug, status;

-- name: AdminSetVariantStock :one
UPDATE product_variants v
SET stock = sqlc.arg(stock)
FROM products p
WHERE v.id = sqlc.arg(id)
  AND p.id = v.product_id
  AND p.tenant_id = sqlc.arg(tenant_id)
RETURNING v.id, v.product_id, v.stock;

-- name: AdminAdjustVariantStock :one
-- Applies a delta against the live stock. No row means the variant is missing
-- or the result would be negative (the caller has already locked the row, so
-- it distinguishes the two).
UPDATE product_variants v
SET stock = v.stock + sqlc.arg(delta)::int
FROM products p
WHERE v.id = sqlc.arg(id)
  AND p.id = v.product_id
  AND p.tenant_id = sqlc.arg(tenant_id)
  AND v.stock + sqlc.arg(delta)::int >= 0
RETURNING v.id, v.product_id, v.stock;

-- name: AdminRecomputeProductStockFlag :one
UPDATE products
SET in_stock   = EXISTS (SELECT 1 FROM product_variants pv WHERE pv.product_id = products.id AND pv.stock > 0),
    updated_at = now()
WHERE products.id = sqlc.arg(id)
  AND products.tenant_id = sqlc.arg(tenant_id)
RETURNING in_stock;

-- name: AdminFindVariantsForImport :many
-- Resolves import row keys (variant ids and SKUs) within the tenant.
SELECT v.id, v.product_id, v.sku
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE (v.id = ANY(sqlc.arg(ids)::uuid[]) OR v.sku = ANY(sqlc.arg(skus)::text[]))
  AND p.tenant_id = sqlc.arg(tenant_id);

-- name: AdminExistingSKUsAnyTenant :many
-- product_variants.sku is globally unique. Returns only the subset of the
-- caller-supplied SKUs that exist anywhere (equivalent to one boolean per SKU),
-- never another tenant's row.
SELECT sku::text
FROM product_variants
WHERE sku = ANY(sqlc.arg(skus)::text[]);

-- name: AdminFindProductsForImport :many
-- Resolves import product ids and slugs within the tenant.
SELECT id, slug
FROM products
WHERE (id = ANY(sqlc.arg(ids)::uuid[]) OR slug = ANY(sqlc.arg(slugs)::text[]))
  AND tenant_id = sqlc.arg(tenant_id);

-- name: AdminSlugsTakenByOtherTenants :many
-- products.slug is globally unique. Returns only the subset of the
-- caller-supplied slugs that another tenant uses (one boolean per slug), never
-- another tenant's row. Phrased with `tenant_id =` so the tenant guard accepts it.
SELECT slug
FROM products
WHERE slug = ANY(sqlc.arg(slugs)::text[])
  AND NOT (tenant_id = sqlc.arg(tenant_id)::uuid);

-- name: GetTenantSetting :one
SELECT value
FROM tenant_settings
WHERE tenant_id = sqlc.arg(tenant_id)
  AND key = sqlc.arg(key);

-- name: AdminExportOrderRows :many
-- Keyset page (created_at DESC, id DESC) over the same filters as AdminListOrders.
SELECT o.id,
       o.order_number,
       o.status,
       o.currency,
       o.pricing_total,
       o.pricing_subtotal,
       o.pricing_discount,
       o.pricing_tax,
       o.pricing_shipping,
       o.applied_voucher_code,
       o.created_at,
       o.updated_at,
       u.name  AS customer_name,
       u.email AS customer_email,
       COALESCE((SELECT SUM(qty) FROM order_items WHERE order_id = o.id), 0)::int AS items_count,
       COALESCE((SELECT status::text FROM payments WHERE order_id = o.id AND tenant_id = o.tenant_id ORDER BY created_at DESC LIMIT 1), '')::text AS payment_status,
       (SELECT courier FROM shipments WHERE order_id = o.id AND tenant_id = o.tenant_id LIMIT 1) AS courier,
       (SELECT tracking_number FROM shipments WHERE order_id = o.id AND tenant_id = o.tenant_id LIMIT 1) AS tracking_number
FROM orders o
LEFT JOIN users u ON u.id = o.user_id
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(status)::order_status IS NULL OR o.status = sqlc.narg(status)::order_status)
  AND (sqlc.narg(search)::text IS NULL
       OR o.order_number ILIKE '%' || sqlc.narg(search)::text || '%'
       OR u.email ILIKE '%' || sqlc.narg(search)::text || '%'
       OR u.name ILIKE '%' || sqlc.narg(search)::text || '%')
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR o.created_at >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR o.created_at < sqlc.narg(end_at)::timestamptz)
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (o.created_at, o.id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg(batch_size)::int;
