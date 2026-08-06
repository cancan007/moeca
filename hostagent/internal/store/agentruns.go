package store

// AgentRun is a manually-launched agent run (from Delivery), persisted so it
// shares the run-history + optimization loop with scheduled runs.
type AgentRun struct {
	ID          int64  `json:"id"`
	Source      string `json:"source"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Branch      string `json:"branch"`
	Task        string `json:"task"`
	Template    string `json:"template"`
	TemplateRef string `json:"templateRef"`
	ContainerID string `json:"containerId"`
	RunID       string `json:"runId"`
	CreatedAt   string `json:"createdAt"`
}

// RecordAgentRun inserts a manual run and returns its id.
func (s *SQLiteStore) RecordAgentRun(r AgentRun) (int64, error) {
	source := r.Source
	if source == "" {
		source = "manual"
	}
	res, err := s.db.Exec(
		`INSERT INTO agent_runs (source, name, repo, branch, task, template, template_ref, container_id, run_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		source, r.Name, r.Repo, r.Branch, r.Task, r.Template, r.TemplateRef, r.ContainerID, r.RunID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AgentRuns returns recent manual runs, newest first.
func (s *SQLiteStore) AgentRuns(limit int) ([]AgentRun, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT id, source, name, repo, branch, task, template, template_ref, container_id, run_id, created_at
		 FROM agent_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRun
	for rows.Next() {
		var r AgentRun
		if err := rows.Scan(&r.ID, &r.Source, &r.Name, &r.Repo, &r.Branch, &r.Task, &r.Template, &r.TemplateRef, &r.ContainerID, &r.RunID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
