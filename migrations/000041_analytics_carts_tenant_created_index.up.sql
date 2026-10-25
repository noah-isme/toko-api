-- Serves the conversion report's per-tenant carts range count.
-- This file must stay a single statement: golang-migrate's postgres driver
-- sends the file as one simple query without BEGIN, so CONCURRENTLY works only
-- while no second statement turns it into an implicit transaction block.
-- CONCURRENTLY does not block cart writes from the old binaries during deploy.
-- On failure see docs/ops/RUNBOOK.md "Migrations 000041/000042".
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_carts_tenant_created ON carts(tenant_id, created_at);
