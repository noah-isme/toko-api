DROP TABLE IF EXISTS payment_events;
ALTER TABLE payments
    DROP COLUMN IF EXISTS channel,
    DROP COLUMN IF EXISTS intent_token,
    DROP COLUMN IF EXISTS redirect_url,
    DROP COLUMN IF EXISTS amount,
    DROP COLUMN IF EXISTS expires_at;
