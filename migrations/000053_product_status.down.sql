SET LOCAL lock_timeout = '5s';
ALTER TABLE products DROP COLUMN IF EXISTS status;
