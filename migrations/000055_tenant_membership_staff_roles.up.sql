SET LOCAL lock_timeout = '5s';

-- Preserve today's effective access: a global admin (users.roles contains
-- 'admin') was admin in every tenant they belonged to. 'ADMIN' is already
-- allowed by the old CHECK, so the backfill runs first, under row locks only,
-- and the ACCESS EXCLUSIVE lock taken by the ALTER below is held just for the
-- constraint swap until commit.
UPDATE tenant_memberships tm SET role = 'ADMIN', updated_at = now()
  FROM users u WHERE u.id = tm.user_id AND tm.role = 'CUSTOMER' AND 'admin' = ANY(u.roles);

-- Staff RBAC: widen the tenant membership role set. NOT VALID skips the
-- validation scan while ACCESS EXCLUSIVE is held (the new set is a superset of
-- the old one); 000056 validates it without blocking readers.
ALTER TABLE tenant_memberships DROP CONSTRAINT tenant_memberships_role_check;
ALTER TABLE tenant_memberships ADD CONSTRAINT tenant_memberships_role_check
  CHECK (role IN ('OWNER','ADMIN','MANAGER','CATALOG','FULFILLMENT','SUPPORT','ANALYST','CUSTOMER')) NOT VALID;
