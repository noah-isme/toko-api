-- name: RefreshSalesDaily :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY mv_sales_daily;

-- name: RefreshTopProducts :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY mv_top_products;

-- name: GetSalesDailyRange :many
SELECT day::timestamptz AS day,
       paid_orders,
       all_orders,
       COALESCE(revenue, 0) AS revenue
FROM mv_sales_daily
WHERE day >= sqlc.arg(start_date)::timestamptz
  AND day < sqlc.arg(end_date)::timestamptz
ORDER BY day ASC;

-- name: GetTopProducts :many
SELECT product_id, qty_sold, gross
FROM mv_top_products
ORDER BY qty_sold DESC
LIMIT sqlc.arg(limit_count) OFFSET sqlc.arg(offset_rows);
