DROP INDEX IF EXISTS idx_cart_items_campaign;
ALTER TABLE cart_items DROP COLUMN IF EXISTS campaign_id;
