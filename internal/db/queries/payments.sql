-- name: CreatePayment :one
INSERT INTO payments (
    order_id,
    provider,
    channel,
    status,
    provider_payload,
    intent_token,
    redirect_url,
    amount,
    created_at,
    updated_at,
    expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now(), $9)
RETURNING id, order_id, provider, status, provider_payload, created_at, updated_at, channel, intent_token, redirect_url, amount,
         expires_at;

-- name: UpdatePaymentStatus :exec
UPDATE payments
SET status = $2,
    provider_payload = $3,
    updated_at = now()
WHERE id = $1;

-- name: GetLatestPaymentByOrder :one
SELECT id, order_id, provider, status, provider_payload, created_at, updated_at, channel, intent_token, redirect_url, amount,
       expires_at
FROM payments
WHERE order_id = $1
ORDER BY created_at DESC
LIMIT 1;

-- name: InsertPaymentEvent :exec
INSERT INTO payment_events (payment_id, status, payload)
VALUES ($1, $2, $3);
