-- Link an executed occurrence to the agent run it launched, so the Daily UI can
-- show real artifacts: the worktree (repo/branch) the agent produced files in,
-- and the sandbox container whose A2A logs are the session record.
ALTER TABLE schedule_runs ADD COLUMN repo TEXT NOT NULL DEFAULT '';
ALTER TABLE schedule_runs ADD COLUMN branch TEXT NOT NULL DEFAULT '';
ALTER TABLE schedule_runs ADD COLUMN container_id TEXT NOT NULL DEFAULT '';
