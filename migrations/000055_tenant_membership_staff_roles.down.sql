SET LOCAL lock_timeout = '5s';

-- Fail closed: staff roles that did not exist before map to CUSTOMER, and the
-- ADMIN rows created by the up backfill revert to CUSTOMER.
UPDATE tenant_memberships SET role = 'CUSTOMER', updated_at = now()
  WHERE role IN ('MANAGER','CATALOG','FULFILLMENT','SUPPORT','ANALYST');
UPDATE tenant_memberships tm SET role = 'CUSTOMER', updated_at = now()
  FROM users u WHERE u.id = tm.user_id AND tm.role = 'ADMIN' AND 'admin' = ANY(u.roles);

ALTER TABLE tenant_memberships DROP CONSTRAINT tenant_memberships_role_check;
ALTER TABLE tenant_memberships ADD CONSTRAINT tenant_memberships_role_check
  CHECK (role IN ('OWNER','ADMIN','CUSTOMER')) NOT VALID;
