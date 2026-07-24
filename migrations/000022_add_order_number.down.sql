DROP INDEX IF EXISTS idx_orders_order_number_unique;
ALTER TABLE orders DROP COLUMN IF EXISTS order_number;
