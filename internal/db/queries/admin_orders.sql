-- Admin order and voucher management queries. These power the admin dashboard
-- list/detail views, which need cross-user visibility that the customer-facing
-- order queries deliberately do not provide.

-- name: AdminCountOrders :one
SELECT COUNT(*)
FROM orders o
LEFT JOIN users u ON u.id = o.user_id
WHERE (sqlc.narg(status)::order_status IS NULL OR o.status = sqlc.arg(status)::order_status)
  AND (sqlc.narg(search)::text IS NULL
       OR o.order_number ILIKE '%' || sqlc.arg(search)::text || '%'
       OR u.email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR u.name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR o.created_at >= sqlc.arg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR o.created_at < sqlc.arg(end_at)::timestamptz);

-- name: AdminListOrders :many
SELECT o.id,
       o.order_number,
       o.user_id,
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
       COALESCE((SELECT status::text FROM payments WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1), '')::text AS payment_status,
       (SELECT courier FROM shipments WHERE order_id = o.id LIMIT 1) AS courier,
       (SELECT tracking_number FROM shipments WHERE order_id = o.id LIMIT 1) AS tracking_number
FROM orders o
LEFT JOIN users u ON u.id = o.user_id
WHERE (sqlc.narg(status)::order_status IS NULL OR o.status = sqlc.arg(status)::order_status)
  AND (sqlc.narg(search)::text IS NULL
       OR o.order_number ILIKE '%' || sqlc.arg(search)::text || '%'
       OR u.email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR u.name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR o.created_at >= sqlc.arg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR o.created_at < sqlc.arg(end_at)::timestamptz)
ORDER BY o.created_at DESC, o.id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: AdminGetOrder :one
SELECT o.id,
       o.order_number,
       o.user_id,
       o.status,
       o.currency,
       o.pricing_total,
       o.pricing_subtotal,
       o.pricing_discount,
       o.pricing_tax,
       o.pricing_shipping,
       o.applied_voucher_code,
       o.shipping_address,
       o.shipping_option,
       o.notes,
       o.created_at,
       o.updated_at,
       u.name  AS customer_name,
       u.email AS customer_email,
       COALESCE((SELECT SUM(qty) FROM order_items WHERE order_id = o.id), 0)::int AS items_count,
       COALESCE((SELECT status::text FROM payments WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1), '')::text AS payment_status,
       (SELECT courier FROM shipments WHERE order_id = o.id LIMIT 1) AS courier,
       (SELECT tracking_number FROM shipments WHERE order_id = o.id LIMIT 1) AS tracking_number
FROM orders o
LEFT JOIN users u ON u.id = o.user_id
WHERE o.id = sqlc.arg(id)
LIMIT 1;

-- name: AdminOrderStats :one
SELECT COUNT(*)::bigint                                                                    AS total_orders,
       COALESCE(SUM(o.pricing_total), 0)::bigint                                           AS total_revenue,
       COUNT(*) FILTER (WHERE o.status = 'PENDING_PAYMENT')::bigint                        AS pending_orders,
       COUNT(*) FILTER (WHERE o.status = 'PAID')::bigint                                   AS paid_orders,
       COUNT(*) FILTER (WHERE o.status IN ('SHIPPED', 'OUT_FOR_DELIVERY'))::bigint         AS shipped_orders,
       COUNT(*) FILTER (WHERE o.status = 'DELIVERED')::bigint                              AS delivered_orders,
       COUNT(*) FILTER (WHERE o.status = 'CANCELLED')::bigint                              AS cancelled_orders,
       COALESCE(AVG(o.pricing_total), 0)::bigint                                           AS average_order_value
FROM orders o
WHERE (sqlc.narg(start_at)::timestamptz IS NULL OR o.created_at >= sqlc.arg(start_at)::timestamptz)
  AND (sqlc.narg(end_at)::timestamptz IS NULL OR o.created_at < sqlc.arg(end_at)::timestamptz);

-- name: AdminCountCustomers :one
SELECT COUNT(*)::bigint FROM users WHERE NOT ('admin' = ANY(roles));

-- name: AdminCountProductsTotal :one
SELECT COUNT(*)::bigint FROM products;

-- name: AdminTopProductsByRevenue :many
SELECT oi.product_id,
       MIN(oi.title)::text            AS title,
       MIN(oi.slug)::text             AS slug,
       SUM(oi.qty)::bigint            AS units_sold,
       SUM(oi.subtotal)::bigint       AS revenue
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
WHERE o.status <> 'CANCELLED'
  AND (sqlc.narg(start_at)::timestamptz IS NULL OR o.created_at >= sqlc.arg(start_at)::timestamptz)
GROUP BY oi.product_id
ORDER BY revenue DESC
LIMIT sqlc.arg(limit_value);

-- name: AdminListVouchers :many
SELECT v.id,
       v.code,
       v.kind,
       v.value,
       v.percent_bps,
       v.min_spend,
       v.usage_limit,
       v.used_count,
       v.per_user_limit,
       v.valid_from,
       v.valid_to,
       v.combinable,
       v.priority,
       v.product_ids,
       v.category_ids,
       v.brand_ids,
       v.created_at,
       v.updated_at
FROM vouchers v
WHERE (sqlc.narg(search)::text IS NULL OR v.code ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(kind)::discount_kind IS NULL OR v.kind = sqlc.arg(kind)::discount_kind)
ORDER BY v.created_at DESC, v.id
LIMIT sqlc.arg(limit_value) OFFSET sqlc.arg(offset_value);

-- name: AdminCountVouchers :one
SELECT COUNT(*)
FROM vouchers v
WHERE (sqlc.narg(search)::text IS NULL OR v.code ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(kind)::discount_kind IS NULL OR v.kind = sqlc.arg(kind)::discount_kind);

-- name: AdminVoucherStats :one
SELECT COUNT(*)::bigint                                                                     AS total_vouchers,
       COUNT(*) FILTER (
           WHERE (valid_from IS NULL OR valid_from <= now())
             AND (valid_to IS NULL OR valid_to >= now())
             AND (usage_limit IS NULL OR used_count < usage_limit)
       )::bigint                                                                            AS active_vouchers,
       COALESCE(SUM(used_count), 0)::bigint                                                 AS total_usage
FROM vouchers;

-- name: AdminDeleteVoucher :execrows
DELETE FROM vouchers WHERE code = sqlc.arg(code);
