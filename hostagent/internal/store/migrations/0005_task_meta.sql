-- Per-Delivery-task metadata (goal + milestones), keyed by repo/branch since a
-- Delivery task is a git worktree, not a stored record. meta holds the JSON.
CREATE TABLE task_meta (
    repo       TEXT NOT NULL,
    branch     TEXT NOT NULL,
    meta       TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (repo, branch)
);
