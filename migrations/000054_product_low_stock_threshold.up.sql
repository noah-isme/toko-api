SET LOCAL lock_timeout = '5s';
ALTER TABLE products ADD COLUMN IF NOT EXISTS low_stock_threshold INT NULL
  CONSTRAINT products_low_stock_threshold_check CHECK (low_stock_threshold IS NULL OR (low_stock_threshold >= 0 AND low_stock_threshold <= 1000000));
