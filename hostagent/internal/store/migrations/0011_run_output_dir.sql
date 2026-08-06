-- Daily runs are not git work. A schedule fires on its own, produces artifacts
-- (a report, a rendered video, a chart) and is reviewed and downloaded from the
-- Daily gallery — none of which involves a repository, a branch or a diff.
--
-- So an occurrence now records the directory its run wrote into, rather than the
-- repo/branch of a worktree it no longer creates. The older repo/branch columns
-- are left in place: rows written before this change still carry them, and
-- dropping a column in SQLite means rebuilding the table for no benefit.
ALTER TABLE schedule_runs ADD COLUMN output_dir TEXT NOT NULL DEFAULT '';
