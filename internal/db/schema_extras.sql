-- Helper definitions for sqlc generation.
CREATE TABLE IF NOT EXISTS analytics_materialized (
        tenant_id UUID
);

-- migrations/000014_audit_logs.up.sql creates this enum inside a DO $$ block so
-- the migration stays idempotent. sqlc's parser does not evaluate DO blocks, so
-- without this declaration it types actor_kind as interface{} and pgx fails with
-- "cannot scan unknown type (OID ...) into *interface {}" when reading
-- audit_logs. Keep the value list in sync with the migration.
CREATE TYPE actor_kind AS ENUM ('user', 'system', 'anonymous');
