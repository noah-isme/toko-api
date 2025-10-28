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
