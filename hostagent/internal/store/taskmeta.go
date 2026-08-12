package store

import (
	"database/sql"
	"encoding/json"
)

// TaskMeta is a Delivery task's goal + milestones + the agent template assigned
// to it (keyed by repo/branch).
type TaskMeta struct {
	Goal       string      `json:"goal"`
	Milestones []Milestone `json:"milestones"`
	// Template is the assigned agent template id ("" = single agent,
	// "__dynamic__" = pick one at run time). Rows written before this field
	// existed unmarshal to "", i.e. single agent.
	Template string `json:"template"`
	// Scope is the knowledge this task's agents may retrieve, named as a node
	// of the Knowledge graph. Absent means no scope: the run searches
	// everything, which is what every task did before this field existed.
	Scope *KnowledgeScope `json:"scope,omitempty"`
}

// GetTaskMeta returns the metadata for a Delivery task (zero value if none).
func (s *SQLiteStore) GetTaskMeta(repo, branch string) (TaskMeta, error) {
	var raw string
	err := s.db.QueryRow(`SELECT meta FROM task_meta WHERE repo = ? AND branch = ?`, repo, branch).Scan(&raw)
	if err == sql.ErrNoRows {
		return TaskMeta{}, nil
	}
	if err != nil {
		return TaskMeta{}, err
	}
	var m TaskMeta
	_ = json.Unmarshal([]byte(raw), &m)
	return m, nil
}

// SetTaskMeta upserts a Delivery task's goal + milestones.
func (s *SQLiteStore) SetTaskMeta(repo, branch string, m TaskMeta) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO task_meta (repo, branch, meta, updated_at) VALUES (?, ?, ?, datetime('now'))
		 ON CONFLICT(repo, branch) DO UPDATE SET meta = excluded.meta, updated_at = excluded.updated_at`,
		repo, branch, string(raw))
	return err
}
