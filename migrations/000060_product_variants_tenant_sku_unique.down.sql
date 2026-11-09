-- Single statement on purpose: DROP INDEX CONCURRENTLY cannot run inside a
-- transaction block. 000061's down has already restored the global constraint.
DROP INDEX CONCURRENTLY IF EXISTS uq_product_variants_tenant_sku;
