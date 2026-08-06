-- Manual agent runs launched from Delivery (bare "エージェント実行" or a template
-- run). Persisted so they share the run-history + optimization loop with
-- scheduled runs. A run carries the produced worktree (repo/branch) and either a
-- container id (bare) or a run id (template DAG), plus the template it used.
CREATE TABLE agent_runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    source       TEXT NOT NULL DEFAULT 'manual',
    name         TEXT NOT NULL DEFAULT '',
    repo         TEXT NOT NULL DEFAULT '',
    branch       TEXT NOT NULL DEFAULT '',
    task         TEXT NOT NULL DEFAULT '',
    template     TEXT NOT NULL DEFAULT '',
    template_ref TEXT NOT NULL DEFAULT '',
    container_id TEXT NOT NULL DEFAULT '',
    run_id       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);

CREATE INDEX idx_agent_runs_time ON agent_runs(created_at DESC);
