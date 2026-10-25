-- Order invoices (PDF) and per-tenant invoice numbering.

-- name: GetOrderForInvoice :one
-- Tenant-scoped; user_id narrows to the order owner (NULL for admin access).
SELECT o.id,
       o.order_number,
       o.user_id,
       o.status,
       o.currency,
       o.pricing_subtotal,
       o.pricing_discount,
       o.pricing_tax,
       o.pricing_shipping,
       o.pricing_total,
       o.applied_voucher_code,
       o.shipping_address,
       o.created_at,
       o.tenant_id,
       t.name AS tenant_name,
       t.contact_email AS tenant_contact_email,
       t.timezone AS tenant_timezone,
       u.name AS customer_name,
       u.email AS customer_email,
       oi.invoice_number,
       oi.issued_at,
       COALESCE((SELECT pm.status::text FROM payments pm WHERE pm.order_id = o.id AND pm.tenant_id = o.tenant_id ORDER BY pm.created_at DESC LIMIT 1), '')::text AS payment_status,
       COALESCE((SELECT pm.channel FROM payments pm WHERE pm.order_id = o.id AND pm.tenant_id = o.tenant_id ORDER BY pm.created_at DESC LIMIT 1), '')::text AS payment_channel
FROM orders o
JOIN tenants t ON t.id = o.tenant_id
LEFT JOIN users u ON u.id = o.user_id
LEFT JOIN order_invoices oi ON oi.order_id = o.id AND oi.tenant_id = o.tenant_id
WHERE o.id = sqlc.arg(id)
  AND o.tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(user_id)::uuid IS NULL OR o.user_id = sqlc.narg(user_id)::uuid);

-- name: LockInvoiceCounter :one
-- Upserts the tenant's counter row and takes its row lock, serializing invoice
-- number assignment per tenant for the rest of the transaction.
INSERT INTO invoice_counters (tenant_id)
VALUES (sqlc.arg(tenant_id))
ON CONFLICT (tenant_id) DO UPDATE SET tenant_id = EXCLUDED.tenant_id
RETURNING last_number;

-- name: GetOrderInvoice :one
SELECT invoice_number, issued_at
FROM order_invoices
WHERE order_id = sqlc.arg(order_id)
  AND tenant_id = sqlc.arg(tenant_id);

-- name: IncrementInvoiceCounter :one
UPDATE invoice_counters
SET last_number = last_number + 1,
    updated_at = now()
WHERE tenant_id = sqlc.arg(tenant_id)
RETURNING last_number;

-- name: InsertOrderInvoice :one
INSERT INTO order_invoices (order_id, tenant_id, invoice_number, issued_at)
VALUES (sqlc.arg(order_id), sqlc.arg(tenant_id), sqlc.arg(invoice_number), sqlc.arg(issued_at))
RETURNING invoice_number, issued_at;

-- name: ListInvoiceItems :many
SELECT oi.title,
       oi.qty,
       oi.unit_price,
       oi.subtotal,
       v.sku
FROM order_items oi
JOIN orders o ON o.id = oi.order_id
LEFT JOIN product_variants v ON v.id = oi.variant_id
WHERE oi.order_id = sqlc.arg(order_id)
  AND o.tenant_id = sqlc.arg(tenant_id)
ORDER BY oi.title ASC, oi.id;

-- name: ListTenantSettingsByKeys :many
SELECT key, value
FROM tenant_settings
WHERE tenant_id = sqlc.arg(tenant_id)
  AND key = ANY(sqlc.arg(keys)::text[]);
