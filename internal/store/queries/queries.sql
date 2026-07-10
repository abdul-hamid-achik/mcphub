-- name: InsertToolCall :exec
INSERT INTO tool_calls (
    ts, server, tool, namespaced, duration_ms, ok, error, args_bytes, result_bytes, est_tokens
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: RecentToolCalls :many
SELECT * FROM tool_calls ORDER BY id DESC LIMIT ?;

-- name: ToolCallStats :many
SELECT
    server,
    tool,
    COUNT(*)                                      AS calls,
    COALESCE(SUM(CASE WHEN ok THEN 0 ELSE 1 END), 0) AS errors,
    CAST(COALESCE(AVG(duration_ms), 0) AS INTEGER)   AS avg_ms,
    COALESCE(SUM(est_tokens), 0)                  AS est_tokens
FROM tool_calls
WHERE ts >= sqlc.arg(since)
GROUP BY server, tool
ORDER BY calls DESC;

-- name: ServerStats :many
SELECT
    server,
    COUNT(*)                                      AS calls,
    COALESCE(SUM(CASE WHEN ok THEN 0 ELSE 1 END), 0) AS errors,
    COALESCE(SUM(est_tokens), 0)                  AS est_tokens,
    CAST(COALESCE(AVG(duration_ms), 0) AS INTEGER)   AS avg_ms
FROM tool_calls
WHERE ts >= sqlc.arg(since)
GROUP BY server
ORDER BY calls DESC;

-- name: TotalStats :one
SELECT
    COUNT(*)                          AS calls,
    COALESCE(SUM(est_tokens), 0)      AS est_tokens,
    COALESCE(SUM(duration_ms), 0)     AS total_ms,
    COALESCE(SUM(CASE WHEN ok THEN 0 ELSE 1 END), 0) AS errors
FROM tool_calls
WHERE ts >= sqlc.arg(since);

-- name: UpsertManaged :exec
INSERT INTO managed_entries (agent, server, applied_at)
VALUES (?, ?, ?)
ON CONFLICT(agent, server) DO UPDATE SET applied_at = excluded.applied_at;

-- name: ListManagedForAgent :many
SELECT * FROM managed_entries WHERE agent = ? ORDER BY server;

-- name: DeleteManaged :exec
DELETE FROM managed_entries WHERE agent = ? AND server = ?;

-- name: ClearManagedForAgent :exec
DELETE FROM managed_entries WHERE agent = ?;

-- name: InsertSyncRun :exec
INSERT INTO sync_runs (ts, agent, mode, servers, dry_run) VALUES (?, ?, ?, ?, ?);

-- name: RecentSyncRuns :many
SELECT * FROM sync_runs ORDER BY id DESC LIMIT ?;

-- name: InsertSpoolResult :exec
INSERT INTO result_spool (call_id, server, tool, created_at, expires_at, payload)
VALUES (?, ?, ?, ?, ?, ?);

-- name: PageSpoolResult :one
SELECT
    server,
    tool,
    expires_at,
    CAST(length(payload) AS INTEGER) AS total_bytes,
    CAST(substr(payload, sqlc.arg(cursor) + 1, sqlc.arg(page_size)) AS BLOB) AS page
FROM result_spool
WHERE call_id = sqlc.arg(call_id);

-- name: PruneExpiredSpoolResults :exec
DELETE FROM result_spool WHERE expires_at <= ?;
