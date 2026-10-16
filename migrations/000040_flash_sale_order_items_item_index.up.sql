SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS idx_flash_sale_order_items_item ON flash_sale_order_items(flash_sale_item_id);
