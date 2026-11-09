-- Admin catalog operations: bulk updates, CSV import lookups, CSV export and
-- low-stock settings. Variant queries scope through products.tenant_id
-- (product_variants.tenant_id, added in 000057, is always the product's tenant).
-- SKUs and product slugs are unique per tenant, so import lookups never need to
-- look at another tenant's rows.

-- name: AdminGetProductsByIDsForUpdate :many
-- Locks the target products in id order so concurrent catalog writers cannot
-- deadlock. FOR NO KEY UPDATE (not FOR UPDATE) still serializes catalog writers
-- against each other but stays compatible with the FOR KEY SHARE locks that
-- foreign-key checks take (inventory_reservations, cart_items,
-- user_product_views), so checkout and add-to-cart neither block on
-- nor deadlock with a running import or bulk request.
SELECT id, slug, title, price, compare_at, status, low_stock_threshold, description, category_id, brand_id
FROM products
WHERE id = ANY(sqlc.arg(ids)::uuid[])
  AND tenant_id = sqlc.arg(tenant_id)
ORDER BY id
FOR NO KEY UPDATE;

-- name: AdminLockVariantsByIDs :many
-- FOR NO KEY UPDATE: serializes against checkout's ReserveVariantStock and
-- other catalog writers, but not against FK checks (cart_items.variant_id,
-- inventory_reservations.variant_id), so add-to-cart does not block on it.
SELECT v.id, v.product_id, v.sku, v.price, v.stock, v.attributes, p.slug
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE v.id = ANY(sqlc.arg(ids)::uuid[])
  AND p.tenant_id = sqlc.arg(tenant_id)
ORDER BY v.id
FOR NO KEY UPDATE OF v;

-- name: AdminLockProductsOfVariants :many
-- Locks the products owning the listed variants, in product id order. Bulk
-- stock takes these before AdminLockVariantsByIDs so every catalog writer
-- (import, bulk price, bulk stock) locks products before variants. It must be
-- FOR NO KEY UPDATE: checkout holds a variant row lock and then takes FOR KEY
-- SHARE on the product (inventory_reservations.product_id FK check), which
-- FOR UPDATE would conflict with and turn into a deadlock cycle.
SELECT p.id
FROM products p
WHERE p.tenant_id = sqlc.arg(tenant_id)
  AND p.id IN (
    SELECT v.product_id FROM product_variants v WHERE v.id = ANY(sqlc.arg(ids)::uuid[])
  )
ORDER BY p.id
FOR NO KEY UPDATE OF p;

-- name: AdminLockVariantsByIDsOrProducts :many
-- Locks the listed variants plus every variant of the listed products, in one
-- statement and in id order (bulk price with applyTo=both|variants).
SELECT v.id, v.product_id, v.sku, v.price, v.stock, v.attributes, p.slug
FROM product_variants v
JOIN products p ON p.id = v.product_id
WHERE (v.id = ANY(sqlc.arg(ids)::uuid[]) OR v.product_id = ANY(sqlc.arg(product_ids)::uuid[]))
  AND p.tenant_id = sqlc.arg(tenant_id)
ORDER BY v.id
FOR NO KEY UPDATE OF v;

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

-- name: AdminFindProductsForImport :many
-- Resolves import product ids and slugs within the tenant.
SELECT id, slug
FROM products
WHERE (id = ANY(sqlc.arg(ids)::uuid[]) OR slug = ANY(sqlc.arg(slugs)::text[]))
  AND tenant_id = sqlc.arg(tenant_id);

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
