-- product_sales is a derived counter maintained only by the worker refresh job
-- (internal/catalog/popularity.go). Paid states must match the popularity rule:
-- PAID, PACKED, SHIPPED, OUT_FOR_DELIVERY, DELIVERED (CANCELLED and unpaid states excluded).

-- name: ListTenantIDsForProductSales :many
SELECT id FROM tenants ORDER BY id;

-- name: TryLockProductSalesTenant :one
-- Transaction-scoped advisory lock so only one refresher works on a tenant at a time.
SELECT pg_try_advisory_xact_lock(hashtextextended('product_sales:' || sqlc.arg(tenant_id)::uuid::text, 0)) AS locked;

-- name: UpsertProductSalesForTenant :execrows
INSERT INTO product_sales (product_id, tenant_id, units_sold, updated_at)
SELECT p.id, p.tenant_id, agg.units, now()
FROM (
    SELECT oi.product_id, SUM(oi.qty)::bigint AS units
    FROM order_items oi
    JOIN orders o ON o.id = oi.order_id
    WHERE o.tenant_id = sqlc.arg(tenant_id)
      AND o.status IN ('PAID', 'PACKED', 'SHIPPED', 'OUT_FOR_DELIVERY', 'DELIVERED')
    GROUP BY oi.product_id
) agg
JOIN products p ON p.id = agg.product_id AND p.tenant_id = sqlc.arg(tenant_id)
WHERE agg.units > 0
-- No row lock on products: locking every sold product on every run would dirty and
-- WAL-log hot products pages even when nothing changed. A product deleted after this
-- statement's snapshot makes the FK check fail with 23503; RefreshTenant then skips the
-- tenant until the next run.
ON CONFLICT (product_id) DO UPDATE
   SET units_sold = EXCLUDED.units_sold,
       tenant_id  = EXCLUDED.tenant_id,
       updated_at = now()
 WHERE product_sales.units_sold IS DISTINCT FROM EXCLUDED.units_sold
    OR product_sales.tenant_id IS DISTINCT FROM EXCLUDED.tenant_id;

-- name: DeleteStaleProductSalesForTenant :execrows
DELETE FROM product_sales ps
WHERE ps.tenant_id = sqlc.arg(tenant_id)
  AND NOT EXISTS (
    SELECT 1
    FROM order_items oi
    JOIN orders o ON o.id = oi.order_id
    WHERE oi.product_id = ps.product_id
      AND o.tenant_id = sqlc.arg(tenant_id)
      AND o.status IN ('PAID', 'PACKED', 'SHIPPED', 'OUT_FOR_DELIVERY', 'DELIVERED')
      AND oi.qty > 0
  );

-- name: GetProductSalesUnits :one
SELECT units_sold FROM product_sales WHERE product_id = sqlc.arg(product_id) AND tenant_id = sqlc.arg(tenant_id);
