CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    data JSONB NOT NULL DEFAULT '{}'::jsonb,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- List and unread-count queries filter by (user_id, tenant_id) ordered by recency.
CREATE INDEX idx_notifications_user_tenant_created
    ON notifications (user_id, tenant_id, created_at DESC);

-- Partial index to keep unread-count cheap as history grows.
CREATE INDEX idx_notifications_unread
    ON notifications (user_id, tenant_id)
    WHERE read_at IS NULL;
