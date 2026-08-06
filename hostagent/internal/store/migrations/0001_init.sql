-- Schedules: cron-driven tasks. The public id is 'sch-'||seq.
CREATE TABLE schedules (
    seq         INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    cron        TEXT    NOT NULL,
    perspective TEXT    NOT NULL DEFAULT '',
    task        TEXT    NOT NULL DEFAULT '',
    active      INTEGER NOT NULL DEFAULT 1,
    last_run    TEXT    NOT NULL DEFAULT '',
    run_count   INTEGER NOT NULL DEFAULT 0,
    meta        TEXT    NOT NULL DEFAULT '{}',
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);

-- Generic key/value flags (seed guard, per-source pull cursors, ...).
CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
