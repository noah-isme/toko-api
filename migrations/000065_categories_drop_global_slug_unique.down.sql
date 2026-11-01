SET LOCAL lock_timeout = '5s';
-- Restores the database-wide unique constraint(s) on categories. Builds the index under
-- ACCESS EXCLUSIVE (reads and writes on categories wait for the build).
--
-- This down FAILS with a unique violation ("could not create unique index") once two
-- tenants use the same value, which per-tenant uniqueness allows. Find the clashes
-- with the query in docs/ops/RUNBOOK.md ("Migrations 000057-000070"), rename one side,
-- then run the down again. Nothing is changed when it fails (one transaction), but
-- golang-migrate leaves the version dirty at 64: clear it with `migrate force 65` first.
ALTER TABLE categories ADD CONSTRAINT categories_slug_key UNIQUE (slug);
