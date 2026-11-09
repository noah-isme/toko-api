SET LOCAL lock_timeout = '5s';
ALTER TABLE products ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'published'
  CONSTRAINT products_status_check CHECK (status IN ('draft', 'published', 'archived'));
