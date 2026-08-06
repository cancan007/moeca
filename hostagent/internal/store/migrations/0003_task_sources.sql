-- User-configured Daily pull providers (added at runtime via Settings, in
-- addition to any declared in the config file).
CREATE TABLE task_sources (
    name       TEXT PRIMARY KEY,
    type       TEXT NOT NULL,
    created_at TEXT NOT NULL
);
