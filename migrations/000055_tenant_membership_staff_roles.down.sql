SET LOCAL lock_timeout = '5s';

-- Same lock order as the up file: ACCESS EXCLUSIVE first, so the updates below
-- never hold row locks while the ALTER waits to upgrade (deadlock-free).
LOCK TABLE tenant_memberships IN ACCESS EXCLUSIVE MODE;

-- Fail closed: staff roles that did not exist before map to CUSTOMER, and the
-- ADMIN rows created by the up backfill revert to CUSTOMER. Both updates are
-- valid under the old and the new CHECK, so they run before the ALTER.
UPDATE tenant_memberships SET role = 'CUSTOMER', updated_at = now()
  WHERE role IN ('MANAGER','CATALOG','FULFILLMENT','SUPPORT','ANALYST');
UPDATE tenant_memberships tm SET role = 'CUSTOMER', updated_at = now()
  FROM users u WHERE u.id = tm.user_id AND tm.role = 'ADMIN' AND 'admin' = ANY(u.roles);

-- The restored three-value CHECK is added NOT VALID (no scan under ACCESS
-- EXCLUSIVE); every row already satisfies it after the updates above and new
-- writes are checked. Run
--   ALTER TABLE tenant_memberships VALIDATE CONSTRAINT tenant_memberships_role_check;
-- afterwards to mark it validated again (see docs/ops/RUNBOOK.md).
ALTER TABLE tenant_memberships DROP CONSTRAINT tenant_memberships_role_check;
ALTER TABLE tenant_memberships ADD CONSTRAINT tenant_memberships_role_check
  CHECK (role IN ('OWNER','ADMIN','CUSTOMER')) NOT VALID;
