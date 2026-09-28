-- Chat: a standing conversation with an agent, at the same granularity as
-- Delivery and Daily.
--
-- A conversation is not git work and not a schedule. Like a Daily occurrence it
-- writes into a plain output directory rather than a worktree (see
-- launchScheduledRun), but unlike one it is a sequence: every turn is its own
-- run, and what the next turn sends depends on what the earlier ones said. So
-- the transcript is durable state and lives here, beside the schedules, rather
-- than in the webview's localStorage where a long conversation would eventually
-- be dropped on the floor by a storage quota.
--
-- Two properties of this schema are load-bearing for history compaction:
--
--   * A compacted turn is MARKED, never deleted. `dropped` means "not sent to
--     the model any more", not "gone" — the row keeps its text so the summary
--     stays auditable, the UI can expand what was replaced, and the same
--     history can be re-compressed under different settings later. Compaction
--     that destroys the transcript cannot be undone or reviewed, which is the
--     one thing it must not do.
--   * A summary is a turn of its own (`kind = 'summary'`) carrying the range it
--     stands for (`covers_from` / `covers_to`), so "which turns does this
--     briefing replace" is a fact in the row rather than something recomputed
--     from whatever is marked dropped at read time.
CREATE TABLE IF NOT EXISTS chat_conversations (
  seq            INTEGER PRIMARY KEY AUTOINCREMENT,
  title          TEXT NOT NULL DEFAULT '',
  -- The bound agent template, as a ref the frontend can compile: "solo:<id>",
  -- "static:<id>" or the Dynamic sentinel. Stored as the ref rather than an
  -- agent id so binding a multi-agent template later needs no migration.
  template_ref   TEXT NOT NULL DEFAULT '',
  template_label TEXT NOT NULL DEFAULT '',
  -- The knowledge scope, named as a graph node exactly as a schedule names it
  -- (kind: global | organization | project). Empty kind means no scope was
  -- chosen, and such a run retrieves nothing — the same default a task starts
  -- with, for the same reason.
  scope_kind     TEXT NOT NULL DEFAULT '',
  scope_id       TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chat_turns (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  conversation_id INTEGER NOT NULL REFERENCES chat_conversations(seq) ON DELETE CASCADE,
  -- Position in the conversation. Unique per conversation so a double submit
  -- cannot interleave. A summary turn's seq is only its identity — where it
  -- reads is decided by covers_to; see turnOrder in chat.go.
  seq             INTEGER NOT NULL,
  kind            TEXT NOT NULL,                -- user | agent | summary
  text            TEXT NOT NULL DEFAULT '',
  -- What the person was replying to, as a JSON array of strings: a paragraph
  -- they selected in an answer, or a region they marked on an image. Stored
  -- beside the message rather than pasted into it, because an agent that cannot
  -- tell "the passage I am asking about" from "what I am telling you" answers the
  -- wrong one — and because the transcript should show the reply the way the
  -- person wrote it.
  quotes          TEXT NOT NULL DEFAULT '',
  -- For an agent turn: the orchestrator run that produced it, the working
  -- directory it ran in, and how that run ended. A turn still running has status
  -- 'running' and no text yet. The directory is the CONVERSATION's — every turn
  -- shares one, so an agent can read what the previous turn wrote — and is
  -- recorded per turn so an old turn still resolves after the layout changes.
  run_id          TEXT NOT NULL DEFAULT '',
  output_dir      TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL DEFAULT '',
  -- Display name of the agent that answered, so an old turn still renders with
  -- the agent it actually ran as after the binding is changed.
  agent           TEXT NOT NULL DEFAULT '',
  -- Rough token estimate for this turn's text (char/4, the same metric the
  -- agent runtime falls back to). Drives the context ring without asking a
  -- provider to count tokens for a ring.
  tokens_est      INTEGER NOT NULL DEFAULT 0,
  -- 1 = replaced by a summary and no longer sent to the model. The text stays.
  dropped         INTEGER NOT NULL DEFAULT 0,
  -- Only on kind='summary': the inclusive turn range this briefing stands for.
  covers_from     INTEGER NOT NULL DEFAULT 0,
  covers_to       INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  UNIQUE(conversation_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_chat_turns_conv ON chat_turns(conversation_id, seq);
