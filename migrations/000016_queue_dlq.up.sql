CREATE TABLE IF NOT EXISTS queue_dlq (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind TEXT NOT NULL,
  idem_key TEXT NOT NULL,
  payload BYTEA NOT NULL,
  attempts INT NOT NULL,
  last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_queue_dlq_kind ON queue_dlq(kind, created_at DESC);
