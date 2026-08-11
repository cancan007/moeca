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

// Occurrence statuses.
//
//	executed — the schedule fired and its run was launched. Recorded the moment
//	           the run is submitted, so this is the status of a run still going.
//	missed   — its cron passed while the app was down.
//	failed   — it could not be launched, or the run itself failed.
//	done     — the run finished and produced files.
//	empty    — the run finished, nothing failed, and it produced nothing.
//
// The last two exist because 'executed' was previously terminal: it was written
// at submission and never revisited, so a run that died, or one where every
// stage exited 0 and wrote nothing at all, both showed as a schedule that had
// run. "It completed" and "it made something" are different facts and the
// history has to be able to tell them apart.
const (
	RunStatusExecuted = "executed"
	RunStatusMissed   = "missed"
	RunStatusFailed   = "failed"
	RunStatusDone     = "done"
	RunStatusEmpty    = "empty"
)

// SetOccurrenceOutcome records how a launched run actually ended. Addressed by
// run id because that is the only handle the watcher has once the run is the
// controller's business.
func (s *SQLiteStore) SetOccurrenceOutcome(runID, status string) error {
	if runID == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE schedule_runs SET status = ? WHERE run_id = ?`, status, runID)
	return err
}

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
