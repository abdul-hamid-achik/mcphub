-- Plan-scoped backup bookkeeping (SPEC §8.3). Each applied sync plan records
-- which backup file captured the pre-apply state of the agent config, so
-- `mcphub sync --rollback <planId>` can restore that exact backup instead of
-- guessing from filesystem timestamps.
CREATE TABLE IF NOT EXISTS plan_backups (
    plan_id     TEXT PRIMARY KEY,
    agent       TEXT NOT NULL,
    config_path TEXT NOT NULL,
    backup_path TEXT NOT NULL,
    created_at  TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_plan_backups_agent
    ON plan_backups (agent, created_at);
