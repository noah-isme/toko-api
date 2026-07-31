-- name: InsertAuditLog :one
INSERT INTO audit_logs (
    actor_kind,
    actor_user_id,
    action,
    resource_type,
    resource_id,
    method,
    path,
    route,
    status,
    ip,
    user_agent,
    request_id,
    metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
) RETURNING id, created_at;

-- name: ListAuditLogs :many
SELECT
    id,
    actor_kind,
    actor_user_id,
    action,
    resource_type,
    resource_id,
    method,
    path,
    route,
    status,
    ip,
    user_agent,
    request_id,
    metadata,
    created_at
FROM audit_logs
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- Filtered variants back the admin audit-log screen. Each filter is skipped when
-- its parameter is NULL so one query serves every combination.
-- name: ListAuditLogsFiltered :many
SELECT
    id,
    actor_kind,
    actor_user_id,
    action,
    resource_type,
    resource_id,
    method,
    path,
    route,
    status,
    ip,
    user_agent,
    request_id,
    metadata,
    created_at
FROM audit_logs
WHERE (sqlc.narg('actor_user_id')::uuid IS NULL OR actor_user_id = sqlc.narg('actor_user_id')::uuid)
  AND (sqlc.narg('action')::text IS NULL OR action ILIKE '%' || sqlc.narg('action')::text || '%')
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type')::text)
  AND (sqlc.narg('start_date')::timestamptz IS NULL OR created_at >= sqlc.narg('start_date')::timestamptz)
  AND (sqlc.narg('end_date')::timestamptz IS NULL OR created_at <= sqlc.narg('end_date')::timestamptz)
ORDER BY created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAuditLogsFiltered :one
SELECT count(*)
FROM audit_logs
WHERE (sqlc.narg('actor_user_id')::uuid IS NULL OR actor_user_id = sqlc.narg('actor_user_id')::uuid)
  AND (sqlc.narg('action')::text IS NULL OR action ILIKE '%' || sqlc.narg('action')::text || '%')
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type')::text)
  AND (sqlc.narg('start_date')::timestamptz IS NULL OR created_at >= sqlc.narg('start_date')::timestamptz)
  AND (sqlc.narg('end_date')::timestamptz IS NULL OR created_at <= sqlc.narg('end_date')::timestamptz);
