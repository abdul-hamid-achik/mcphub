-- Per-call agent identity and invoke path so stats can split harnesses and
-- mount vs mcphub_call_tool vs detach. Added as nullable-safe defaults because
-- applyMigrations re-runs historically used IF NOT EXISTS; Open now records
-- applied files so this ALTER runs once.
ALTER TABLE tool_calls ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_calls ADD COLUMN via TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tool_calls_agent ON tool_calls (agent);
CREATE INDEX IF NOT EXISTS idx_tool_calls_via ON tool_calls (via);
