-- Single statement on purpose (see the up file): DROP INDEX CONCURRENTLY
-- cannot run inside a transaction block.
DROP INDEX CONCURRENTLY IF EXISTS idx_carts_tenant_created;
