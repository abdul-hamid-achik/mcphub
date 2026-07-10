-- Lossless bounded-result spool. Payloads are complete serialized MCP
-- CallToolResult values and expire after a fixed application-controlled TTL.
CREATE TABLE IF NOT EXISTS result_spool (
    call_id    TEXT PRIMARY KEY,
    server     TEXT NOT NULL,
    tool       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    payload    BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_result_spool_expires_at
    ON result_spool (expires_at);
