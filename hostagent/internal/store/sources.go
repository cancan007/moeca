package store

// TaskSourceRow is a user-configured Daily pull provider.
type TaskSourceRow struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// TaskSources returns the runtime-configured task sources, ordered by name.
func (s *SQLiteStore) TaskSources() ([]TaskSourceRow, error) {
	rows, err := s.db.Query(`SELECT name, type FROM task_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskSourceRow
	for rows.Next() {
		var r TaskSourceRow
		if err := rows.Scan(&r.Name, &r.Type); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddTaskSource upserts a task source by name.
func (s *SQLiteStore) AddTaskSource(name, typ string) error {
	_, err := s.db.Exec(
		`INSERT INTO task_sources (name, type, created_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(name) DO UPDATE SET type = excluded.type`,
		name, typ)
	return err
}

// RemoveTaskSource deletes a task source; the bool reports whether it existed.
func (s *SQLiteStore) RemoveTaskSource(name string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM task_sources WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
