-- Record the orchestrator run id and the template a fired occurrence used, so
-- the run view can show per-stage logs and offer to edit that template's prompt.
ALTER TABLE schedule_runs ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
ALTER TABLE schedule_runs ADD COLUMN template TEXT NOT NULL DEFAULT '';
