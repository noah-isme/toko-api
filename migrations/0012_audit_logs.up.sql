DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'actor_kind') THEN
        CREATE TYPE actor_kind AS ENUM ('user', 'system', 'anonymous');
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_kind actor_kind NOT NULL,
    actor_user_id UUID NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NULL,
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    route TEXT NULL,
    status INT NOT NULL,
    ip TEXT NULL,
    user_agent TEXT NULL,
    request_id TEXT NULL,
    metadata JSONB NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_logs (actor_kind, actor_user_id);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_logs (action);
