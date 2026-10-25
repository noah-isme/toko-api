-- Product alert subscriptions (back-in-stock / price-drop).

-- name: UpsertAlertSubscription :one
-- Creates a subscription or re-arms an existing one for the same target.
INSERT INTO product_alert_subscriptions (tenant_id, user_id, product_id, variant_id, alert_type, price_at_subscribe)
VALUES (sqlc.arg(tenant_id), sqlc.arg(user_id), sqlc.arg(product_id), sqlc.narg(variant_id), sqlc.arg(alert_type), sqlc.narg(price_at_subscribe))
ON CONFLICT (tenant_id, user_id, product_id, COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'::uuid), alert_type)
DO UPDATE SET price_at_subscribe = EXCLUDED.price_at_subscribe,
              notified_at = NULL,
              updated_at = now()
RETURNING id, tenant_id, user_id, product_id, variant_id, alert_type, price_at_subscribe, notified_at, created_at, updated_at;

-- name: GetAlertTarget :one
-- Loads a product (and optionally one of its variants) inside a tenant with
-- everything needed to decide sellability and the current price.
SELECT p.id AS product_id,
       p.title,
       p.slug,
       p.thumbnail,
       p.price,
       p.in_stock,
       (SELECT COUNT(*) FROM product_variants cv WHERE cv.product_id = p.id)::int AS variant_count,
       (SELECT COALESCE(SUM(GREATEST(sv.stock, 0)), 0) FROM product_variants sv WHERE sv.product_id = p.id)::bigint AS total_stock,
       v.id AS variant_id,
       v.sku AS variant_sku,
       v.price AS variant_price,
       v.stock AS variant_stock
FROM products p
LEFT JOIN product_variants v ON v.id = sqlc.narg(variant_id)::uuid AND v.product_id = p.id
WHERE p.id = sqlc.arg(product_id)
  AND p.tenant_id = sqlc.arg(tenant_id);

-- name: ListAlertSubscriptionsForUser :many
SELECT s.id,
       s.product_id,
       s.variant_id,
       s.alert_type,
       s.price_at_subscribe,
       s.notified_at,
       s.created_at,
       s.updated_at,
       p.title AS product_title,
       p.slug AS product_slug,
       p.thumbnail AS product_thumbnail,
       p.price AS product_price,
       p.in_stock AS product_in_stock,
       v.sku AS variant_sku,
       v.price AS variant_price,
       v.stock AS variant_stock
FROM product_alert_subscriptions s
JOIN products p ON p.id = s.product_id AND p.tenant_id = s.tenant_id
LEFT JOIN product_variants v ON v.id = s.variant_id AND v.product_id = s.product_id
WHERE s.tenant_id = sqlc.arg(tenant_id)
  AND s.user_id = sqlc.arg(user_id)
  AND (sqlc.narg(product_id)::uuid IS NULL OR s.product_id = sqlc.narg(product_id)::uuid)
ORDER BY s.created_at DESC, s.id DESC
LIMIT sqlc.arg(row_limit) OFFSET sqlc.arg(row_offset);

-- name: CountAlertSubscriptionsForUser :one
SELECT COUNT(*)
FROM product_alert_subscriptions s
JOIN products p ON p.id = s.product_id AND p.tenant_id = s.tenant_id
WHERE s.tenant_id = sqlc.arg(tenant_id)
  AND s.user_id = sqlc.arg(user_id)
  AND (sqlc.narg(product_id)::uuid IS NULL OR s.product_id = sqlc.narg(product_id)::uuid);

-- name: DeleteAlertSubscription :execrows
DELETE FROM product_alert_subscriptions
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND user_id = sqlc.arg(user_id);

-- name: ListPendingAlertEvents :many
-- System-level drainer: claims unprocessed outbox rows across all tenants.
-- Every row carries its tenant_id and all follow-up queries are filtered by it.
SELECT id, tenant_id, product_id, variant_id, alert_type, created_at
FROM product_alert_events
WHERE processed_at IS NULL
ORDER BY id
LIMIT sqlc.arg(batch_size)
FOR UPDATE SKIP LOCKED;

-- name: MarkAlertEventsProcessed :exec
UPDATE product_alert_events
SET processed_at = now()
WHERE id = ANY(sqlc.arg(ids)::bigint[])
  AND processed_at IS NULL;

-- name: ListPendingBackInStockSubscriptions :many
-- Returns ONLY actionable rows (target sellable right now), so LIMIT and the
-- row lock never spend budget on subscriptions that cannot fire. Sellability
-- mirrors internal/cart: variant-level = product in_stock AND variant stock > 0;
-- product-level = in_stock AND (no variants OR any variant with stock > 0).
-- Correlated EXISTS is used because FOR UPDATE forbids aggregates.
SELECT s.id,
       s.tenant_id,
       s.user_id,
       s.product_id,
       s.variant_id,
       u.email AS user_email,
       u.name AS user_name,
       p.title AS product_title,
       p.slug AS product_slug,
       (CASE WHEN s.variant_id IS NULL THEN p.price ELSE v.price END)::bigint AS current_price
FROM product_alert_subscriptions s
JOIN users u ON u.id = s.user_id
JOIN products p ON p.id = s.product_id AND p.tenant_id = s.tenant_id
LEFT JOIN product_variants v ON v.id = s.variant_id AND v.product_id = s.product_id
WHERE s.tenant_id = sqlc.arg(tenant_id)
  AND s.product_id = sqlc.arg(product_id)
  AND s.alert_type = 'BACK_IN_STOCK'
  AND s.notified_at IS NULL
  AND p.in_stock
  AND (
        (s.variant_id IS NOT NULL AND v.stock > 0)
     OR (s.variant_id IS NULL AND (
            NOT EXISTS (SELECT 1 FROM product_variants nv WHERE nv.product_id = p.id)
         OR EXISTS (SELECT 1 FROM product_variants av WHERE av.product_id = p.id AND av.stock > 0)
        ))
  )
ORDER BY s.created_at, s.id
LIMIT sqlc.arg(max_rows)
FOR UPDATE OF s SKIP LOCKED;

-- name: ListPendingPriceDropSubscriptions :many
-- Returns ONLY actionable rows (current effective price below the price at
-- subscribe time); see ListPendingBackInStockSubscriptions for why.
SELECT s.id,
       s.tenant_id,
       s.user_id,
       s.product_id,
       s.variant_id,
       s.price_at_subscribe,
       u.email AS user_email,
       u.name AS user_name,
       p.title AS product_title,
       p.slug AS product_slug,
       (CASE WHEN s.variant_id IS NULL THEN p.price ELSE v.price END)::bigint AS current_price
FROM product_alert_subscriptions s
JOIN users u ON u.id = s.user_id
JOIN products p ON p.id = s.product_id AND p.tenant_id = s.tenant_id
LEFT JOIN product_variants v ON v.id = s.variant_id AND v.product_id = s.product_id
WHERE s.tenant_id = sqlc.arg(tenant_id)
  AND s.product_id = sqlc.arg(product_id)
  AND s.alert_type = 'PRICE_DROP'
  AND s.notified_at IS NULL
  AND s.price_at_subscribe IS NOT NULL
  AND (CASE WHEN s.variant_id IS NULL THEN p.price ELSE v.price END) < s.price_at_subscribe
ORDER BY s.created_at, s.id
LIMIT sqlc.arg(max_rows)
FOR UPDATE OF s SKIP LOCKED;

-- name: MarkAlertSubscriptionNotified :execrows
UPDATE product_alert_subscriptions
SET notified_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND notified_at IS NULL;
