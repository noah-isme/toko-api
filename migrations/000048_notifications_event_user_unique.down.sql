-- Single statement on purpose: DROP INDEX CONCURRENTLY cannot run inside a
-- transaction block. Roll binaries back first (CreateNotification's
-- ON CONFLICT clause needs this index).
DROP INDEX CONCURRENTLY IF EXISTS uq_notifications_event_user;
