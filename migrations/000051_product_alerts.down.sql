SET LOCAL lock_timeout = '5s';

DROP TRIGGER IF EXISTS trg_product_alert_product_update ON products;
DROP TRIGGER IF EXISTS trg_product_alert_variant_update ON product_variants;
DROP TRIGGER IF EXISTS trg_product_alert_variant_insert ON product_variants;
DROP FUNCTION IF EXISTS product_alert_product_changed();
DROP FUNCTION IF EXISTS product_alert_variant_changed();
DROP TABLE IF EXISTS product_alert_deliveries;
DROP TABLE IF EXISTS product_alert_events;
DROP TABLE IF EXISTS product_alert_subscriptions;
