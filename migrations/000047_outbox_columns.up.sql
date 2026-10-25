-- 000047: outbox columns on domain_events + notifications.event_id.
--
-- Roll binaries back before running this down: the new InsertDomainEvent and
-- CreateNotification name these columns.
--
-- Locking. golang-migrate sends this whole file in one ExecContext (simple
-- query protocol), which would otherwise run every statement in one implicit
-- transaction and keep the ACCESS EXCLUSIVE lock of the first ALTER while the
-- second one waits. Each ALTER therefore runs in its own explicit transaction:
-- neither table stays locked while the other is being locked. Both ALTERs are
-- metadata-only on PG >= 11 (no rewrite, no backfill UPDATE): now() is STABLE,
-- so it is stored as a fast default evaluated once at ALTER time. The lock is
-- held for milliseconds; lock_timeout bounds only the wait to acquire it, and
-- a timeout fails the migration fast (the Job retries; IF NOT EXISTS makes a
-- rerun safe).
--
-- Rollout. dispatched_at DEFAULT now() means existing rows, and rows written by
-- old binaries during the rollout (which still fan out inline), are born
-- "dispatched" and are never picked up by the outbox relay. Only the new
-- InsertDomainEvent writes dispatched_at = NULL explicitly.
--
-- Dirty-state recovery: if the second transaction fails after the first one
-- committed, version 47 is marked dirty. Run `migrate force 46` and then
-- `migrate up` again; the IF NOT EXISTS clauses skip the committed part.
--
-- The unique index on notifications (event_id, user_id) is built CONCURRENTLY
-- in 000048, never here.

BEGIN;
SET LOCAL lock_timeout = '5s';
ALTER TABLE domain_events
  ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMPTZ DEFAULT now(),
  ADD COLUMN IF NOT EXISTS dispatch_attempts INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_dispatch_error TEXT;
COMMIT;

BEGIN;
SET LOCAL lock_timeout = '5s';
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS event_id UUID;
COMMIT;
