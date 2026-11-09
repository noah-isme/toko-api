SET LOCAL lock_timeout = '5s';

-- Lock footprint. The CHECK swap below needs ACCESS EXCLUSIVE on tenant_memberships,
-- and golang-migrate runs this file as one implicit transaction, so that lock is held
-- until commit whatever the statement order. Taking it FIRST, before the backfill
-- row-locks anything, means the transaction never upgrades a weaker lock. Without it
-- the backfill UPDATE holds ROW EXCLUSIVE and row locks while the ALTER waits for
-- ACCESS EXCLUSIVE behind concurrent sessions, and any of them that then needs a
-- backfilled row (an UPDATE, or a staff change's SELECT ... FOR UPDATE) waits on the
-- migration: Postgres aborts one side with "deadlock detected" and golang-migrate
-- stops dirty at 55. SHARE ROW EXCLUSIVE would not be enough: it still admits
-- ACCESS SHARE and ROW SHARE holders, and those block the later upgrade the same way.
-- Cost: membership reads (every authenticated request) wait for the backfill, which
-- touches only the memberships of global admins; see docs/ops/RUNBOOK.md.
LOCK TABLE tenant_memberships IN ACCESS EXCLUSIVE MODE;

-- Preserve today's effective access: a global admin (users.roles contains
-- 'admin') was admin in every tenant they belonged to. 'ADMIN' is already
-- allowed by the old CHECK, so the backfill can run before the constraint swap.
UPDATE tenant_memberships tm SET role = 'ADMIN', updated_at = now()
  FROM users u WHERE u.id = tm.user_id AND tm.role = 'CUSTOMER' AND 'admin' = ANY(u.roles);

-- Staff RBAC: widen the tenant membership role set. NOT VALID skips the
-- validation scan while ACCESS EXCLUSIVE is held (the new set is a superset of
-- the old one); 000056 validates it without blocking readers.
ALTER TABLE tenant_memberships DROP CONSTRAINT tenant_memberships_role_check;
ALTER TABLE tenant_memberships ADD CONSTRAINT tenant_memberships_role_check
  CHECK (role IN ('OWNER','ADMIN','MANAGER','CATALOG','FULFILLMENT','SUPPORT','ANALYST','CUSTOMER')) NOT VALID;
