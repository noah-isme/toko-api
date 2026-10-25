-- name: ListOrderItemsForStock :many
SELECT product_id, variant_id, qty, slug
FROM order_items
WHERE order_id = $1;

-- name: DecrementVariantStock :exec
UPDATE product_variants
SET stock = GREATEST(0, stock - sqlc.arg(qty))
WHERE id = sqlc.arg(id);

-- Reserve stock at order creation. The predicate makes concurrent checkouts
-- fail instead of silently overselling the last units.
-- name: ReserveVariantStock :one
UPDATE product_variants
SET stock = stock - sqlc.arg(qty)
WHERE id = sqlc.arg(id)
  AND stock >= sqlc.arg(qty)
RETURNING id;

-- name: ReleaseVariantStock :exec
UPDATE product_variants
SET stock = stock + sqlc.arg(qty)
WHERE id = sqlc.arg(id);

-- Reserve flash-sale quota at checkout. sold_count includes active order
-- reservations; the update predicate is the concurrency guard.
-- name: ReserveFlashSaleItem :one
WITH updated AS (
  UPDATE flash_sale_items i
  SET sold_count = i.sold_count + sqlc.arg(qty)
  FROM flash_sale_campaigns c
  WHERE i.campaign_id = sqlc.arg(campaign_id)
    AND i.product_id = sqlc.arg(product_id)
    AND c.id = i.campaign_id
    AND c.tenant_id = sqlc.arg(tenant_id)
    AND c.status IN ('SCHEDULED', 'ACTIVE')
    AND c.starts_at <= now()
    AND c.ends_at > now()
    AND (i.stock_limit IS NULL OR i.sold_count + sqlc.arg(qty) <= i.stock_limit)
  RETURNING i.id
)
INSERT INTO flash_sale_order_items (order_id, flash_sale_item_id, qty, status)
SELECT sqlc.arg(order_id), id, sqlc.arg(qty), 'RESERVED'
FROM updated
RETURNING id, order_id, flash_sale_item_id, qty, status, created_at, updated_at;

-- name: CommitFlashSaleReservations :exec
UPDATE flash_sale_order_items
SET status = 'COMMITTED', updated_at = now()
WHERE order_id = sqlc.arg(order_id)
  AND status = 'RESERVED';

-- Release quota reserved by an unpaid, expired, or cancelled order.
-- name: ReleaseFlashSaleReservations :exec
WITH released AS (
  UPDATE flash_sale_order_items
  SET status = 'RELEASED', updated_at = now()
  WHERE order_id = sqlc.arg(order_id)
    AND status = 'RESERVED'
  RETURNING flash_sale_item_id, qty
)
UPDATE flash_sale_items item
SET sold_count = GREATEST(0, item.sold_count - released.qty)
FROM released
WHERE item.id = released.flash_sale_item_id;

-- name: CreateInventoryReservation :one
INSERT INTO inventory_reservations (tenant_id, order_id, product_id, variant_id, qty, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListInventoryReservationsByOrder :many
SELECT *
FROM inventory_reservations
WHERE order_id = $1
ORDER BY id;

-- Phase A of the checkout-expiry job: expired reservations left on orders that
-- are already terminal (CANCELLED/EXPIRED). Reservations of payable orders are
-- released only when the order itself expires. Ordered by variant so stock
-- rows are locked in ascending variant_id; SKIP LOCKED so the sweep never
-- waits on a transaction that holds the order.
-- name: ListOrphanExpiredReservationsForTenant :many
SELECT r.*
FROM inventory_reservations r
JOIN orders o ON o.id = r.order_id
WHERE r.tenant_id = sqlc.arg(tenant_id)
  AND o.tenant_id = sqlc.arg(tenant_id)
  AND r.status = 'RESERVED'
  AND r.expires_at <= now()
  AND o.status IN ('CANCELLED', 'EXPIRED')
ORDER BY r.variant_id, r.id
LIMIT sqlc.arg(batch_size)
FOR UPDATE OF r SKIP LOCKED;

-- Observability only: expired reservations still RESERVED on orders that went
-- on to be paid or fulfilled.
-- name: CountAnomalousExpiredReservationsForTenant :one
SELECT count(*)
FROM inventory_reservations r
JOIN orders o ON o.id = r.order_id
WHERE r.tenant_id = sqlc.arg(tenant_id)
  AND o.tenant_id = sqlc.arg(tenant_id)
  AND r.status = 'RESERVED'
  AND r.expires_at <= now()
  AND o.status NOT IN ('PENDING_PAYMENT', 'CANCELLED', 'EXPIRED');

-- Reserved stock of one order, locked in ascending variant_id. Callers hold
-- the order row lock (GetOrderByIDForUpdate) first.
-- name: ListReservedInventoryByOrderForUpdate :many
SELECT *
FROM inventory_reservations
WHERE order_id = $1
  AND status = 'RESERVED'
ORDER BY variant_id, id
FOR UPDATE;

-- Locks the flash-sale items that still hold RESERVED quota for the given
-- orders, in ascending id, before ReleaseFlashSaleReservations decrements them.
-- name: LockFlashSaleItemsForOrders :many
SELECT i.id
FROM flash_sale_items i
WHERE i.id IN (
  SELECT f.flash_sale_item_id
  FROM flash_sale_order_items f
  WHERE f.order_id = ANY(sqlc.arg(order_ids)::uuid[])
    AND f.status = 'RESERVED'
)
ORDER BY i.id
FOR UPDATE OF i;

-- Checkout locks the flash-sale items it is about to reserve, in ascending id,
-- after all variant rows. Matching the two id lists independently can lock a
-- few extra (campaign, product) rows; that is harmless and keeps the order.
-- name: LockFlashSaleItemsForCampaignProducts :many
SELECT i.id
FROM flash_sale_items i
WHERE i.campaign_id = ANY(sqlc.arg(campaign_ids)::uuid[])
  AND i.product_id = ANY(sqlc.arg(product_ids)::uuid[])
ORDER BY i.id
FOR UPDATE OF i;

-- Phase B candidates (no locks; each order is re-checked under its row lock).
-- Deadline = the latest provider deadline over the order's payments, else
-- created_at + fallback TTL. A Midtrans intent created before the release
-- that sends the Snap expiry (no response.ExpirySynced marker) stays payable
-- at Midtrans for its own default window, so while it is PENDING its deadline
-- is at least created_at + legacy_window. Orders whose first payment proof is
-- younger than the proof hold are skipped.
-- name: ListExpiredUnpaidOrderCandidates :many
SELECT o.id
FROM orders o
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.status = 'PENDING_PAYMENT'
  AND COALESCE(
        (SELECT max(CASE
                      WHEN p.provider = 'midtrans'
                       AND p.status = 'PENDING'
                       AND (p.provider_payload -> 'response' ->> 'ExpirySynced') IS DISTINCT FROM 'true'
                      THEN GREATEST(p.expires_at, p.created_at + make_interval(secs => sqlc.arg(legacy_window_sec)::int))
                      ELSE p.expires_at
                    END)
         FROM payments p
         WHERE p.order_id = o.id),
        o.created_at + make_interval(secs => sqlc.arg(fallback_ttl_sec)::int))
      + make_interval(secs => sqlc.arg(grace_sec)::int) <= now()
  AND COALESCE(
        (SELECT min(pp.created_at) FROM payment_proofs pp WHERE pp.order_id = o.id),
        '-infinity'::timestamptz
      ) <= now() - make_interval(secs => sqlc.arg(proof_hold_sec)::int)
ORDER BY o.created_at, o.id
LIMIT sqlc.arg(batch_size);

-- Phase B re-check under the order row lock. ErrNoRows means the order is
-- locked by another transaction or is no longer pending. payment_deadline
-- uses the same rule as ListExpiredUnpaidOrderCandidates (NULL without
-- payments).
-- name: LockPendingOrderForExpiry :one
SELECT o.id,
       o.user_id,
       o.tenant_id,
       o.order_number,
       o.created_at,
       lp.id         AS payment_id,
       (SELECT max(CASE
                     WHEN p.provider = 'midtrans'
                      AND p.status = 'PENDING'
                      AND (p.provider_payload -> 'response' ->> 'ExpirySynced') IS DISTINCT FROM 'true'
                     THEN GREATEST(p.expires_at, p.created_at + make_interval(secs => sqlc.arg(legacy_window_sec)::int))
                     ELSE p.expires_at
                   END)
        FROM payments p
        WHERE p.order_id = o.id)::timestamptz AS payment_deadline,
       (SELECT min(pp.created_at) FROM payment_proofs pp WHERE pp.order_id = o.id)::timestamptz AS first_proof_at,
       (SELECT count(*) FROM payment_proofs pp WHERE pp.order_id = o.id)::int AS proof_count
FROM orders o
LEFT JOIN LATERAL (
  SELECT p.id
  FROM payments p
  WHERE p.order_id = o.id
  ORDER BY p.created_at DESC
  LIMIT 1
) lp ON true
WHERE o.id = sqlc.arg(id)
  AND o.tenant_id = sqlc.arg(tenant_id)
  AND o.status = 'PENDING_PAYMENT'
FOR UPDATE OF o SKIP LOCKED;

-- name: TransitionInventoryReservation :one
UPDATE inventory_reservations
SET status = sqlc.arg(to_status), updated_at = now()
WHERE id = sqlc.arg(id) AND status = sqlc.arg(from_status)
RETURNING *;

-- name: IncrementVoucherUsageByCode :exec
UPDATE vouchers
SET used_count = used_count + 1
WHERE code = $1
  AND (usage_limit IS NULL OR used_count < usage_limit);
