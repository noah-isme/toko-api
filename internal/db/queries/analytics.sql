-- Revenue rule (docs/contracts/analytics.md "Revenue rule"), shared by every
-- analytics/admin revenue query:
--   refunded = non-FAILED refunds (PENDING and SUCCEEDED) summed per order;
--   revenue order iff status IN (PAID, PACKED, SHIPPED, OUT_FOR_DELIVERY,
--   DELIVERED) AND it is not fully refunded (no refund, or refunded < total);
--   order-level revenue = pricing_total - refunded (net).

-- name: GetSalesDailyRange :many
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
)
SELECT (o.created_at AT TIME ZONE sqlc.arg(tz)::text)::date AS day,
       COUNT(*) FILTER (WHERE o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total))::bigint AS paid_orders,
       COUNT(*)::bigint AS all_orders,
       COALESCE(SUM(o.pricing_total - COALESCE(r.amount, 0)) FILTER (WHERE o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)), 0)::bigint AS revenue
FROM orders o
LEFT JOIN refunded r ON r.order_id = o.id
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.created_at >= sqlc.arg(start_date)::timestamptz
  AND o.created_at < sqlc.arg(end_date)::timestamptz
GROUP BY 1
ORDER BY 1;

-- name: GetTopProducts :many
-- Item-level: gross order_items.subtotal of revenue orders (partially refunded
-- orders keep their full item value; fully refunded orders are excluded).
WITH refunded AS (
    SELECT rf.order_id, SUM(rf.amount)::bigint AS amount
    FROM refunds rf
    WHERE rf.tenant_id = sqlc.arg(tenant_id) AND rf.status <> 'FAILED'
    GROUP BY rf.order_id
)
SELECT oi.product_id,
       SUM(oi.qty)::bigint      AS qty_sold,
       SUM(oi.subtotal)::bigint AS gross
FROM order_items oi
JOIN orders o ON o.id = oi.order_id AND o.tenant_id = sqlc.arg(tenant_id)
LEFT JOIN refunded r ON r.order_id = o.id
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.status IN ('PAID','PACKED','SHIPPED','OUT_FOR_DELIVERY','DELIVERED') AND (r.order_id IS NULL OR r.amount < o.pricing_total)
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR o.created_at >= sqlc.narg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR o.created_at < sqlc.narg(end_at)::timestamptz)
GROUP BY oi.product_id
ORDER BY qty_sold DESC, oi.product_id
LIMIT sqlc.arg(limit_count) OFFSET sqlc.arg(offset_rows);
