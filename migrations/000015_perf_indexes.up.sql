-- Example indexes; sesuaikan nama kolom sesuai skema
CREATE INDEX IF NOT EXISTS idx_products_slug ON products(slug);
CREATE INDEX IF NOT EXISTS idx_products_category ON products(category_id, in_stock);
CREATE INDEX IF NOT EXISTS idx_order_created_status ON orders(created_at DESC, status);
CREATE INDEX IF NOT EXISTS idx_order_items_product ON order_items(product_id);
CREATE INDEX IF NOT EXISTS idx_shipment_events_shipment_status ON shipment_events(shipment_id, status);
