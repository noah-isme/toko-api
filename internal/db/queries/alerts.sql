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
-- Events never attempted come first, then the ones whose last pass left them
-- pending longest ago, so one popular product cannot starve other events.
WHERE processed_at IS NULL
ORDER BY last_attempt_at NULLS FIRST, id
LIMIT sqlc.arg(batch_size)
FOR UPDATE SKIP LOCKED;

-- name: MarkAlertEventsAttempted :exec
-- Rotates events that still have claimable subscriptions to the back of the
-- pending order.
UPDATE product_alert_events
SET last_attempt_at = now()
WHERE id = ANY(sqlc.arg(ids)::bigint[])
  AND processed_at IS NULL;

-- name: EnqueueAlertEvent :exec
-- Used by Subscribe when the target became sellable (or cheaper) between the
-- subscription check and its commit, so the drainer re-evaluates it.
INSERT INTO product_alert_events (tenant_id, product_id, variant_id, alert_type)
VALUES (sqlc.arg(tenant_id), sqlc.arg(product_id), sqlc.narg(variant_id), sqlc.arg(alert_type));

-- name: PruneProcessedAlertEvents :execrows
-- System-level retention job (cross-tenant on purpose): deletes a bounded
-- batch of events processed before the cutoff.
DELETE FROM product_alert_events
WHERE id IN (
    SELECT pe.id
    FROM product_alert_events pe
    WHERE pe.processed_at IS NOT NULL
      AND pe.processed_at < sqlc.arg(cutoff)
    ORDER BY pe.processed_at
    LIMIT sqlc.arg(max_rows)
);

-- name: MarkAlertEventsProcessed :exec
UPDATE product_alert_events
SET processed_at = now()
WHERE id = ANY(sqlc.arg(ids)::bigint[])
  AND processed_at IS NULL;

-- name: ListPendingBackInStockSubscriptions :many
-- Returns ONLY actionable rows (target sellable right now), so LIMIT and the
-- row lock never spend budget on subscriptions that cannot fire. The caller
-- asks for one row more than it will claim to detect overflow; that probe row
-- is locked for the rest of the transaction but not claimed. Sellability is
-- exactly the cart rule (internal/cart AddItem): a variant line needs variant
-- stock > 0 (products.in_stock is not consulted); a product-level line needs
-- products.in_stock.
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
  AND (
        (s.variant_id IS NOT NULL AND v.stock > 0)
     OR (s.variant_id IS NULL AND p.in_stock)
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

-- name: InsertAlertDelivery :exec
-- Queues the email for one alert domain event (same transaction as the claim).
INSERT INTO product_alert_deliveries (event_id, tenant_id)
VALUES (sqlc.arg(event_id), sqlc.arg(tenant_id));

-- name: LeaseDueAlertDeliveries :many
-- System-level mailer (cross-tenant on purpose; each row carries tenant_id
-- and follow-up writes filter by it). Leases a bounded batch of due rows by
-- pushing next_attempt_at to lease_until, so a crashed or slow sender's rows
-- become due again only after the lease expires.
WITH due AS (
    SELECT pd.event_id
    FROM product_alert_deliveries pd
    WHERE pd.delivered_at IS NULL
      AND pd.failed_at IS NULL
      AND pd.next_attempt_at <= now()
    ORDER BY pd.next_attempt_at, pd.event_id
    LIMIT sqlc.arg(max_rows)
    FOR UPDATE SKIP LOCKED
)
UPDATE product_alert_deliveries d
SET attempts = d.attempts + 1,
    next_attempt_at = sqlc.arg(lease_until)
FROM due, domain_events e
WHERE d.event_id = due.event_id
  AND e.id = d.event_id
RETURNING d.event_id, d.tenant_id, d.attempts, e.topic, e.aggregate_id, e.payload, e.occurred_at;

-- name: MarkAlertDeliveryDelivered :exec
UPDATE product_alert_deliveries
SET delivered_at = now(),
    last_error = NULL
WHERE event_id = sqlc.arg(event_id)
  AND tenant_id = sqlc.arg(tenant_id);

-- name: MarkAlertDeliveryFailed :exec
-- Schedules a retry at next_attempt_at, or gives up when give_up is true.
UPDATE product_alert_deliveries
SET next_attempt_at = sqlc.arg(next_attempt_at),
    last_error = sqlc.arg(last_error),
    failed_at = CASE WHEN sqlc.arg(give_up)::boolean THEN now() ELSE NULL END
WHERE event_id = sqlc.arg(event_id)
  AND tenant_id = sqlc.arg(tenant_id)
  AND delivered_at IS NULL;

-- name: PruneFinishedAlertDeliveries :execrows
-- System-level retention job: deletes a bounded batch of delivered or
-- abandoned rows older than the cutoff.
DELETE FROM product_alert_deliveries
WHERE event_id IN (
    SELECT pd.event_id
    FROM product_alert_deliveries pd
    WHERE (pd.delivered_at IS NOT NULL AND pd.delivered_at < sqlc.arg(cutoff))
       OR (pd.failed_at IS NOT NULL AND pd.failed_at < sqlc.arg(cutoff))
    LIMIT sqlc.arg(max_rows)
);
