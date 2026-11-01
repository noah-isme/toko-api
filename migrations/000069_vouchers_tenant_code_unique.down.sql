-- Single statement on purpose: DROP INDEX CONCURRENTLY cannot run inside a
-- transaction block. 000070's down has already restored the global constraint.
DROP INDEX CONCURRENTLY IF EXISTS uq_vouchers_tenant_code;
