CREATE MATERIALIZED VIEW IF NOT EXISTS mv_sales_daily AS
SELECT date_trunc('day', created_at) AS day,
       COUNT(*) FILTER (WHERE status = 'PAID') AS paid_orders,
       SUM(pricing_total) FILTER (WHERE status = 'PAID') AS revenue,
       COUNT(*) AS all_orders
FROM orders
GROUP BY 1;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_top_products AS
SELECT oi.product_id,
       SUM(oi.qty) AS qty_sold,
       SUM(oi.subtotal) AS gross
FROM order_items oi
JOIN orders o ON o.id = oi.order_id AND o.status = 'PAID'
GROUP BY oi.product_id;

CREATE INDEX IF NOT EXISTS idx_mv_sales_daily_day ON mv_sales_daily(day);
CREATE INDEX IF NOT EXISTS idx_mv_top_products_qty ON mv_top_products(qty_sold DESC);
