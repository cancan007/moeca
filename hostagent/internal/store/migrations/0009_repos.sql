-- User-configured Delivery repositories (added at runtime via Settings, in
-- addition to any declared in the config file's `repos`). ci_command is a JSON
-- array of strings (may be NULL/empty when no CI is configured).
CREATE TABLE repos (
    name        TEXT PRIMARY KEY,
    path        TEXT NOT NULL,
    target      TEXT NOT NULL,
    ci_command  TEXT,
    created_at  TEXT NOT NULL
);
