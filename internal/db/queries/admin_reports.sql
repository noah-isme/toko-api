-- Admin reports (docs/contracts/analytics.md sections 9.4-9.7).
-- Every statement is tenant-scoped on each orders/carts/products/categories/
-- refunds reference and uses the half-open range [start_at, end_at).
-- Revenue rule: see analytics.sql. Item-level figures (category/product) sum
-- gross order_items.subtotal of revenue orders, so partially refunded orders
-- keep their full item value and fully refunded orders are excluded.
-- Order-level figures (conversion, customers) use net pricing_total.

-- name: AdminReportSalesByCategory :many
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
)
SELECT c.id                                          AS category_id,
       COALESCE(c.name, 'Uncategorized')::text       AS category_name,
       c.slug                                        AS category_slug,
       SUM(oi.qty)::bigint                           AS units_sold,
       COUNT(DISTINCT o.id)::bigint                  AS orders,
       SUM(oi.subtotal)::bigint                      AS revenue
FROM order_items oi
JOIN orders o ON o.id = oi.order_id AND o.tenant_id = sqlc.arg(tenant_id)
LEFT JOIN refunded r ON r.order_id = o.id
LEFT JOIN products p ON p.id = oi.product_id AND p.tenant_id = sqlc.arg(tenant_id)
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = sqlc.arg(tenant_id)
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
  AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz
GROUP BY c.id, c.name, c.slug
ORDER BY revenue DESC, category_name, c.id;

-- name: AdminReportSalesByProduct :many
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
)
SELECT oi.product_id,
       MIN(oi.title)::text                           AS title,
       MIN(oi.slug)::text                            AS slug,
       c.id                                          AS category_id,
       COALESCE(c.name, 'Uncategorized')::text       AS category_name,
       SUM(oi.qty)::bigint                           AS units_sold,
       COUNT(DISTINCT o.id)::bigint                  AS orders,
       SUM(oi.subtotal)::bigint                      AS revenue
FROM order_items oi
JOIN orders o ON o.id = oi.order_id AND o.tenant_id = sqlc.arg(tenant_id)
LEFT JOIN refunded r ON r.order_id = o.id
LEFT JOIN products p ON p.id = oi.product_id AND p.tenant_id = sqlc.arg(tenant_id)
LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = sqlc.arg(tenant_id)
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
  AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz
  AND (sqlc.narg(category_id)::uuid IS NULL OR c.id = sqlc.narg(category_id)::uuid)
GROUP BY oi.product_id, c.id, c.name
ORDER BY revenue DESC, oi.product_id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: AdminReportSalesByProductCount :one
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
)
SELECT COUNT(*)::bigint
FROM (
    SELECT oi.product_id
    FROM order_items oi
    JOIN orders o ON o.id = oi.order_id AND o.tenant_id = sqlc.arg(tenant_id)
    LEFT JOIN refunded r ON r.order_id = o.id
    LEFT JOIN products p ON p.id = oi.product_id AND p.tenant_id = sqlc.arg(tenant_id)
    LEFT JOIN categories c ON c.id = p.category_id AND c.tenant_id = sqlc.arg(tenant_id)
    WHERE o.tenant_id = sqlc.arg(tenant_id)
      AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
      AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz
      AND (sqlc.narg(category_id)::uuid IS NULL OR c.id = sqlc.narg(category_id)::uuid)
    GROUP BY oi.product_id, c.id
) grouped;

-- name: AdminReportConversion :one
-- Aggregate without GROUP BY: always exactly one row, zeros for an empty range.
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
)
SELECT (SELECT COUNT(*)
        FROM carts ct
        WHERE ct.tenant_id = sqlc.arg(tenant_id)
          AND ct.created_at >= sqlc.arg(start_at)::timestamptz
          AND ct.created_at < sqlc.arg(end_at)::timestamptz)::bigint AS carts_created,
       COUNT(o.id)::bigint                                                          AS checkouts_started,
       COUNT(o.id) FILTER (WHERE o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total))::bigint AS paid_orders,
       COUNT(o.id) FILTER (WHERE o.status = 'PENDING_PAYMENT')::bigint              AS pending_orders,
       COUNT(o.id) FILTER (WHERE o.status = 'CANCELLED')::bigint                    AS cancelled_orders,
       COUNT(o.id) FILTER (WHERE o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND r.amount >= o.pricing_total)::bigint AS refunded_orders,
       COALESCE(SUM(o.pricing_total - COALESCE(r.amount, 0)) FILTER (WHERE o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)), 0)::bigint AS paid_revenue,
       COALESCE(SUM(r.amount), 0)::bigint                                           AS refunded_amount
FROM orders o
LEFT JOIN refunded r ON r.order_id = o.id
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz;

-- name: AdminReportCustomersSummary :one
-- The guest figures are scalar subqueries so the statement still returns one
-- row (all zeros) when the range has no customer orders.
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
),
first_orders AS (
    SELECT o.user_id, MIN(o.created_at) AS first_at
    FROM orders o
    LEFT JOIN refunded r ON r.order_id = o.id
    WHERE o.tenant_id = sqlc.arg(tenant_id)
      AND o.user_id IS NOT NULL
      AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
    GROUP BY o.user_id
),
ranged AS (
    SELECT o.user_id, (o.pricing_total - COALESCE(r.amount, 0))::bigint AS net_total
    FROM orders o
    LEFT JOIN refunded r ON r.order_id = o.id
    WHERE o.tenant_id = sqlc.arg(tenant_id)
      AND o.user_id IS NOT NULL
      AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
      AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz
),
guests AS (
    SELECT COUNT(*)::bigint AS guest_orders,
           COALESCE(SUM(o.pricing_total - COALESCE(r.amount, 0)), 0)::bigint AS guest_revenue
    FROM orders o
    LEFT JOIN refunded r ON r.order_id = o.id
    WHERE o.tenant_id = sqlc.arg(tenant_id)
      AND o.user_id IS NULL
      AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
      AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz
)
SELECT COUNT(DISTINCT rg.user_id) FILTER (WHERE fo.first_at >= sqlc.arg(start_at)::timestamptz)::bigint AS new_customers,
       COUNT(DISTINCT rg.user_id) FILTER (WHERE fo.first_at < sqlc.arg(start_at)::timestamptz)::bigint  AS returning_customers,
       COUNT(*) FILTER (WHERE fo.first_at >= sqlc.arg(start_at)::timestamptz)::bigint                  AS orders_from_new,
       COUNT(*) FILTER (WHERE fo.first_at < sqlc.arg(start_at)::timestamptz)::bigint                   AS orders_from_returning,
       COALESCE(SUM(rg.net_total) FILTER (WHERE fo.first_at >= sqlc.arg(start_at)::timestamptz), 0)::bigint AS revenue_from_new,
       COALESCE(SUM(rg.net_total) FILTER (WHERE fo.first_at < sqlc.arg(start_at)::timestamptz), 0)::bigint  AS revenue_from_returning,
       (SELECT g.guest_orders FROM guests g)::bigint                                                   AS guest_orders,
       (SELECT g.guest_revenue FROM guests g)::bigint                                                  AS guest_revenue
FROM ranged rg
JOIN first_orders fo ON fo.user_id = rg.user_id;

-- name: AdminReportCustomersDaily :many
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
),
first_orders AS (
    SELECT o.user_id, MIN(o.created_at) AS first_at
    FROM orders o
    LEFT JOIN refunded r ON r.order_id = o.id
    WHERE o.tenant_id = sqlc.arg(tenant_id)
      AND o.user_id IS NOT NULL
      AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
    GROUP BY o.user_id
)
SELECT (o.created_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day,
       COUNT(DISTINCT o.user_id) FILTER (WHERE (fo.first_at AT TIME ZONE sqlc.arg(tz)::text)::date = (o.created_at AT TIME ZONE sqlc.arg(tz)::text)::date)::bigint AS new_customers,
       COUNT(DISTINCT o.user_id) FILTER (WHERE (fo.first_at AT TIME ZONE sqlc.arg(tz)::text)::date < (o.created_at AT TIME ZONE sqlc.arg(tz)::text)::date)::bigint AS returning_customers,
       COUNT(*)::bigint                                   AS orders,
       COALESCE(SUM(o.pricing_total - COALESCE(r.amount, 0)), 0)::bigint AS revenue
FROM orders o
JOIN first_orders fo ON fo.user_id = o.user_id
LEFT JOIN refunded r ON r.order_id = o.id
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.user_id IS NOT NULL
  AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
  AND o.created_at >= sqlc.arg(start_at)::timestamptz AND o.created_at < sqlc.arg(end_at)::timestamptz
GROUP BY 1
ORDER BY 1;
