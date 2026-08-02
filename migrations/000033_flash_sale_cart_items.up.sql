ALTER TABLE cart_items
  ADD COLUMN IF NOT EXISTS campaign_id UUID REFERENCES flash_sale_campaigns(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_cart_items_campaign ON cart_items(campaign_id);
