-- Tickets pulled from external sources (Jira / Trello / Notion / ...) for the
-- Daily screen. Source-native payload is kept in `raw` (JSON) for flexibility.
CREATE TABLE pulled_tickets (
    id         TEXT PRIMARY KEY,
    source     TEXT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    url        TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT '',
    repo       TEXT NOT NULL DEFAULT '',
    branch     TEXT NOT NULL DEFAULT '',
    labels     TEXT NOT NULL DEFAULT '[]',
    raw        TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL DEFAULT '',
    pulled_at  TEXT NOT NULL
);

CREATE INDEX idx_pulled_source ON pulled_tickets(source, state);
