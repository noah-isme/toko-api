SET LOCAL lock_timeout = '5s';

-- SHARE UPDATE EXCLUSIVE only: does not block reads or writes of tenant_memberships.
ALTER TABLE tenant_memberships VALIDATE CONSTRAINT tenant_memberships_role_check;
