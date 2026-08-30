// Package store is the host agent's durable state, backed by an embedded
// SQLite database (pure-Go driver, no cgo). It replaces the previous in-memory
// maps so schedules and pulled tickets survive restarts. An empty path opens an
// in-memory database (used by tests); a file path persists under the app data
// directory.
package store

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// KnowledgeScope names the part of the Knowledge graph a schedule may read.
//
// Kind "global" carries no id and means the knowledge everyone shares. It
// resolves to an EMPTY group set, not to "everything": globally-scoped sources
// reach a caller regardless of the groups it holds, so "entitled to no group"
// already means "only what is everyone's". The tier needs no special case in
// the retrieval filter — it is what that filter already does.
type KnowledgeScope struct {
	Kind string `json:"kind"` // "global" | "organization" | "project"
	ID   string `json:"id,omitempty"`
}

// Milestone is one checkpoint of a schedule's goal.
type Milestone struct {
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// Schedule is a cron-driven task. It doubles as the JSON shape returned by the
// host agent's /schedules API, so the field tags must stay stable. Goal and
// Milestones live in the flexible `meta` JSON column (no schema change).
type Schedule struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Cron        string      `json:"cron"`
	Perspective string      `json:"perspective"`
	Task        string      `json:"task"`
	Active      bool        `json:"active"`
	LastRun     string      `json:"lastRun"`
	RunCount    int         `json:"runCount"`
	Goal        string      `json:"goal"`
	Milestones  []Milestone `json:"milestones"`
	// TemplateLabel is a display name for the bound agent template.
	TemplateLabel string `json:"templateLabel"`
	// TemplateRef identifies the bound template for the edit loop, e.g.
	// "solo:builder" or "static:g1".
	TemplateRef string `json:"templateRef"`
	// RunSpec is the compiled orchestrator run (stages DAG minus taskId/
	// worktreePath) the frontend produced from the bound template. When present
	// a fired occurrence runs it via the sandbox controller's /run.
	RunSpec json.RawMessage `json:"runSpec,omitempty"`
	// Scope is the knowledge this schedule's agents may retrieve, named as a
	// node of the Knowledge graph rather than as a group list. Groups under a
	// node change as the graph is edited, so the node is what a schedule can
	// mean for longer than a week; it is resolved to groups at launch.
	//
	// Absent means no scope was chosen, and such a run retrieves nothing: the
	// gateway refuses a session that never stated an entitlement. Reaching the
	// knowledge shared with everyone is the "global" scope, chosen explicitly.
	Scope *KnowledgeScope `json:"scope,omitempty"`
	// Attachments are files the author gave this schedule, copied into the run's
	// working directory before its agents start. Metadata only — the bytes live
	// beside the database, not in it.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is one file staged into every run of a schedule.
//
// A Daily run starts in an empty directory, which is right for output but
// leaves no way to hand its agents an input — a reference picture, a CSV to
// summarise, a template to fill in. Knowledge cannot serve that: it is a
// searchable corpus shared across schedules, and reaches the sandbox only
// through retrieval. This is the other kind of input, the one that belongs to
// this task and is simply meant to be there when it starts.
type Attachment struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	AddedAt string `json:"addedAt"` // RFC3339
}

// scheduleMeta is what we persist in the schedules.meta JSON column.
//
// A `repo` key appears in rows written before Daily was separated from git. It
// is deliberately absent here: a schedule no longer binds a repository, so the
// key simply drops on the next write rather than being carried forward as a
// field nothing reads.
type scheduleMeta struct {
	Goal          string          `json:"goal,omitempty"`
	Milestones    []Milestone     `json:"milestones,omitempty"`
	TemplateLabel string          `json:"templateLabel,omitempty"`
	TemplateRef   string          `json:"templateRef,omitempty"`
	RunSpec       json.RawMessage `json:"runSpec,omitempty"`
	Scope         *KnowledgeScope `json:"scope,omitempty"`
	Attachments   []Attachment    `json:"attachments,omitempty"`
}

// SQLiteStore is the concrete store. A single connection is used so an
// in-memory database stays consistent and file writes serialise cleanly.
type SQLiteStore struct{ db *sql.DB }

// Open opens (and migrates) the database at path. Pass ":memory:" for a
// throwaway in-memory database.
func Open(path string) (*SQLiteStore, error) {
	// Foreign keys are off by default in SQLite, which would make the knowledge
	// tables' ON DELETE CASCADE silently decorative — deleting a group would
	// leave its memberships and relations behind, and those relations would
	// still be traversed during retrieval. Enabling the pragma is safe for the
	// existing schema because no table before 0010 declares a foreign key.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Close releases the database.
func (s *SQLiteStore) Close() error { return s.db.Close() }

func seqOf(id string) int64 {
	n, _ := strconv.ParseInt(strings.TrimPrefix(id, "sch-"), 10, 64)
	return n
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// List returns all schedules ordered by creation.
func (s *SQLiteStore) List() ([]*Schedule, error) {
	rows, err := s.db.Query(
		`SELECT seq, name, cron, perspective, task, active, last_run, run_count, meta FROM schedules ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// Create inserts a schedule and returns it with its assigned "sch-<n>" ID.
func (s *SQLiteStore) Create(sc *Schedule) (*Schedule, error) {
	meta, err := json.Marshal(scheduleMeta{Goal: sc.Goal, Milestones: sc.Milestones, TemplateLabel: sc.TemplateLabel, TemplateRef: sc.TemplateRef, RunSpec: sc.RunSpec, Scope: sc.Scope, Attachments: sc.Attachments})
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`INSERT INTO schedules (name, cron, perspective, task, active, meta, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		sc.Name, sc.Cron, sc.Perspective, sc.Task, b2i(sc.Active), string(meta))
	if err != nil {
		return nil, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	sc.ID = "sch-" + strconv.FormatInt(seq, 10)
	return sc, nil
}

// Update rewrites a schedule's mutable fields (identified by ID) and returns the
// updated row (nil if the id is unknown). Used to re-sync a bound template's
// compiled run when its per-granularity prompt is edited.
func (s *SQLiteStore) Update(sc *Schedule) (*Schedule, error) {
	meta, err := json.Marshal(scheduleMeta{Goal: sc.Goal, Milestones: sc.Milestones, TemplateLabel: sc.TemplateLabel, TemplateRef: sc.TemplateRef, RunSpec: sc.RunSpec, Scope: sc.Scope, Attachments: sc.Attachments})
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`UPDATE schedules SET name=?, cron=?, perspective=?, task=?, active=?, meta=?, updated_at=datetime('now') WHERE seq=?`,
		sc.Name, sc.Cron, sc.Perspective, sc.Task, b2i(sc.Active), string(meta), seqOf(sc.ID))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.get(sc.ID)
}

// ByID returns a schedule (nil if not found).
func (s *SQLiteStore) ByID(id string) (*Schedule, error) { return s.get(id) }

// Delete removes a schedule; the bool reports whether a row existed.
func (s *SQLiteStore) Delete(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM schedules WHERE seq = ?`, seqOf(id))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Toggle flips a schedule's active flag and returns the updated row (nil if the
// id is unknown).
func (s *SQLiteStore) Toggle(id string) (*Schedule, error) {
	if _, err := s.db.Exec(
		`UPDATE schedules SET active = 1 - active, updated_at = datetime('now') WHERE seq = ?`, seqOf(id),
	); err != nil {
		return nil, err
	}
	return s.get(id)
}

// RecordRun stamps last_run and increments run_count. The bool reports whether
// the schedule existed.
func (s *SQLiteStore) RecordRun(id string, at time.Time) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE schedules SET last_run = ?, run_count = run_count + 1, updated_at = datetime('now') WHERE seq = ?`,
		at.UTC().Format(time.RFC3339), seqOf(id))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *SQLiteStore) get(id string) (*Schedule, error) {
	row := s.db.QueryRow(
		`SELECT seq, name, cron, perspective, task, active, last_run, run_count, meta FROM schedules WHERE seq = ?`,
		seqOf(id))
	sc, err := scanSchedule(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sc, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSchedule(sc scanner) (*Schedule, error) {
	var seq int64
	var active int
	var meta string
	out := &Schedule{}
	if err := sc.Scan(&seq, &out.Name, &out.Cron, &out.Perspective, &out.Task, &active, &out.LastRun, &out.RunCount, &meta); err != nil {
		return nil, err
	}
	out.ID = "sch-" + strconv.FormatInt(seq, 10)
	out.Active = active != 0
	if meta != "" {
		var m scheduleMeta
		if json.Unmarshal([]byte(meta), &m) == nil {
			out.Goal = m.Goal
			out.Milestones = m.Milestones
			out.TemplateLabel = m.TemplateLabel
			out.TemplateRef = m.TemplateRef
			out.RunSpec = m.RunSpec
			out.Scope = m.Scope
			out.Attachments = m.Attachments
		}
	}
	return out, nil
}

// GetState reads an app_state value ("" if unset).
func (s *SQLiteStore) GetState(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_state WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetState upserts an app_state value.
func (s *SQLiteStore) SetState(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO app_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// SeedOnce inserts example schedules exactly once in the database's lifetime,
// guarded by an app_state flag. Deleting all schedules does NOT trigger a
// reseed on the next open (the previous behaviour, which discarded user edits).
func (s *SQLiteStore) SeedOnce(examples []*Schedule) error {
	done, err := s.GetState("schedules_seeded")
	if err != nil {
		return err
	}
	if done == "1" {
		return nil
	}
	for _, e := range examples {
		if _, err := s.Create(e); err != nil {
			return err
		}
	}
	return s.SetState("schedules_seeded", "1")
}
