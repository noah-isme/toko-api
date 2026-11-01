-- Single statement on purpose: DROP INDEX CONCURRENTLY cannot run inside a
-- transaction block. 000065's down has already restored the global constraint.
DROP INDEX CONCURRENTLY IF EXISTS uq_categories_tenant_slug;
