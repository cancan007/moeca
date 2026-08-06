-- One row per scheduled occurrence of a Daily schedule. Occurrences that fire
-- while the app is running are 'executed'; occurrences whose time passed while
-- the app was down are backfilled as 'missed' (未実行) on next startup.
CREATE TABLE schedule_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    schedule_id  TEXT NOT NULL,          -- 'sch-<n>'
    name         TEXT NOT NULL,          -- denormalized so history survives schedule deletion
    perspective  TEXT NOT NULL DEFAULT '',
    scheduled_at TEXT NOT NULL,          -- RFC3339, the minute the occurrence was due (UTC)
    status       TEXT NOT NULL,          -- 'executed' | 'missed'
    created_at   TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_runs_unique ON schedule_runs(schedule_id, scheduled_at);
CREATE INDEX idx_runs_time ON schedule_runs(scheduled_at);
