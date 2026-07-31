-- Rollback seed users migration
-- Deletes only the seeded users by email

BEGIN;

DELETE FROM users WHERE email IN (
  'admin@toko.com',
  'noah@toko.com',
  'budi@example.com',
  'siti@example.com',
  'andi@example.com',
  'dewi@example.com',
  'eko@example.com',
  'fajar@example.com',
  'gita@example.com',
  'hendra@example.com',
  'indah@example.com',
  'joko@example.com'
);

COMMIT;