BEGIN;

DROP INDEX IF EXISTS idx_email_verifications_user;
DROP TABLE IF EXISTS email_verifications;

ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
ALTER TABLE users DROP COLUMN IF EXISTS phone;

COMMIT;
