-- mcphub local intelligence store.
-- Every proxied tool call, sync operation, and managed-entry bookkeeping row
-- lives here so `mcphub stats` and the Studio TUI can surface usage analytics
-- (which servers/tools are actually used, latency, error rates, token cost).

-- tool_calls records every tool invocation the gateway proxies to a downstream
-- MCP server. est_tokens is a cheap heuristic (bytes / 4) so we can reason
-- about how much context each server/tool costs across agents.
CREATE TABLE IF NOT EXISTS tool_calls (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    ts           TEXT    NOT NULL,            -- RFC3339Nano
    server       TEXT    NOT NULL,            -- downstream server name
    tool         TEXT    NOT NULL,            -- downstream tool name
    namespaced   TEXT    NOT NULL,            -- name exposed by the gateway (server__tool)
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    ok           BOOLEAN NOT NULL DEFAULT 1,  -- 1 success, 0 error
    error        TEXT    NOT NULL DEFAULT '',
    args_bytes   INTEGER NOT NULL DEFAULT 0,
    result_bytes INTEGER NOT NULL DEFAULT 0,
    est_tokens   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_server ON tool_calls (server);
CREATE INDEX IF NOT EXISTS idx_tool_calls_ts     ON tool_calls (ts);

-- managed_entries tracks which downstream servers mcphub wrote into which
-- agent harness, so a later `sync` can prune entries it previously owned
-- without clobbering servers the user added by hand.
CREATE TABLE IF NOT EXISTS managed_entries (
    agent      TEXT NOT NULL,                 -- harness id (claude, opencode, codex, ...)
    server     TEXT NOT NULL,                 -- server name written into that harness
    applied_at TEXT NOT NULL,
    PRIMARY KEY (agent, server)
);

-- sync_runs is an audit log of every `mcphub sync` invocation.
CREATE TABLE IF NOT EXISTS sync_runs (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      TEXT    NOT NULL,
    agent   TEXT    NOT NULL,
    mode    TEXT    NOT NULL,                 -- gateway | direct
    servers TEXT    NOT NULL,                 -- comma-joined server names
    dry_run BOOLEAN NOT NULL DEFAULT 1
);
