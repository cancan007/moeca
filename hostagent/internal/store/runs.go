package store

import "time"

// ScheduleRun is one occurrence of a schedule. When it launched an agent run,
// OutputDir is the directory that run produced its artifacts in.
type ScheduleRun struct {
	ID          int64  `json:"id"`
	ScheduleID  string `json:"scheduleId"`
	Name        string `json:"name"`
	Perspective string `json:"perspective"`
	ScheduledAt string `json:"scheduledAt"` // RFC3339
	Status      string `json:"status"`      // executed | missed | failed
	// OutputDir is the host directory mounted into the run's sandbox as /work,
	// and therefore where its artifacts are. Daily runs are not git work — a
	// schedule produces a report or a rendered video, not a branch to review —
	// so an occurrence points at a plain directory rather than a worktree.
	OutputDir   string `json:"outputDir"`
	ContainerID string `json:"containerId"` // single-sandbox run (no template)
	RunID       string `json:"runId"`       // orchestrator run (template DAG)
	Template    string `json:"template"`    // display label of the template used
	// Repo and Branch are only ever populated on rows written before Daily was
	// separated from git. Nothing sets them now; they are read back so an old
	// occurrence still renders.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// Occurrence statuses. 'executed' = the schedule fired (and, when a repo is
// bound, an agent run was launched); 'missed' = its cron passed while the app
// was down; 'failed' = it fired but launching the agent run errored.
const (
	RunStatusExecuted = "executed"
	RunStatusMissed   = "missed"
	RunStatusFailed   = "failed"
)

// RecordOccurrence inserts one occurrence. Idempotent per (schedule, minute):
// a duplicate (same schedule_id + scheduled_at) is ignored, so a live 'executed'
// tick is never overwritten by a later 'missed' backfill and vice versa.
func (s *SQLiteStore) RecordOccurrence(r ScheduleRun, at time.Time) error {
	_, err := s.db.Exec(
		`INSERT INTO schedule_runs (schedule_id, name, perspective, scheduled_at, status, repo, branch, container_id, run_id, template, output_dir, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(schedule_id, scheduled_at) DO NOTHING`,
		r.ScheduleID, r.Name, r.Perspective, at.UTC().Format(time.RFC3339), r.Status, r.Repo, r.Branch, r.ContainerID, r.RunID, r.Template, r.OutputDir)
	return err
}

// runColumns is the column list every occurrence read shares, so a new field
// cannot be added to one query and forgotten in the other.
const runColumns = `id, schedule_id, name, perspective, scheduled_at, status, repo, branch, container_id, run_id, template, output_dir`

func scanRun(sc interface{ Scan(...any) error }) (ScheduleRun, error) {
	var r ScheduleRun
	err := sc.Scan(&r.ID, &r.ScheduleID, &r.Name, &r.Perspective, &r.ScheduledAt, &r.Status,
		&r.Repo, &r.Branch, &r.ContainerID, &r.RunID, &r.Template, &r.OutputDir)
	return r, err
}

// RunByID returns one occurrence. The Daily gallery addresses artifacts by
// occurrence id, so serving a file means looking up which directory that
// occurrence wrote into — the path never comes from the caller.
func (s *SQLiteStore) RunByID(id int64) (ScheduleRun, error) {
	return scanRun(s.db.QueryRow(`SELECT `+runColumns+` FROM schedule_runs WHERE id = ?`, id))
}

// Runs returns recent occurrences, newest scheduled first.
func (s *SQLiteStore) Runs(limit int) ([]ScheduleRun, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT `+runColumns+` FROM schedule_runs ORDER BY scheduled_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduleRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
