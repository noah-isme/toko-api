-- Roll binaries back before running this down: the new InsertDomainEvent and
-- CreateNotification name these columns.
--
-- Each ALTER runs in its own transaction so neither table's ACCESS EXCLUSIVE
-- lock is held while the other is being acquired. Dropping a column is
-- metadata-only. Dropping notifications.event_id also drops
-- uq_notifications_event_user if 000048 was not rolled back first.

BEGIN;
SET LOCAL lock_timeout = '5s';
ALTER TABLE notifications DROP COLUMN IF EXISTS event_id;
COMMIT;

BEGIN;
SET LOCAL lock_timeout = '5s';
ALTER TABLE domain_events
  DROP COLUMN IF EXISTS last_dispatch_error,
  DROP COLUMN IF EXISTS dispatch_attempts,
  DROP COLUMN IF EXISTS dispatched_at;
COMMIT;
