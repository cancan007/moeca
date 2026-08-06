package store

import (
	"database/sql"
	"encoding/json"
)

// RepoRow is a user-configured Delivery repository (managed at runtime via
// Settings). It mirrors the api.Repo shape but lives in the store package to
// avoid an import cycle; the api layer maps between the two.
type RepoRow struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Target    string   `json:"target"`
	CICommand []string `json:"ciCommand"`
}

// Repos returns the runtime-configured repositories, ordered by name.
func (s *SQLiteStore) Repos() ([]RepoRow, error) {
	rows, err := s.db.Query(`SELECT name, path, target, ci_command FROM repos ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepoRow
	for rows.Next() {
		var r RepoRow
		var ci sql.NullString
		if err := rows.Scan(&r.Name, &r.Path, &r.Target, &ci); err != nil {
			return nil, err
		}
		if ci.Valid && ci.String != "" {
			_ = json.Unmarshal([]byte(ci.String), &r.CICommand)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddRepo upserts a repository by name. An empty ciCommand is stored as NULL.
func (s *SQLiteStore) AddRepo(name, path, target string, ciCommand []string) error {
	var ci any
	if len(ciCommand) > 0 {
		b, err := json.Marshal(ciCommand)
		if err != nil {
			return err
		}
		ci = string(b)
	}
	_, err := s.db.Exec(
		`INSERT INTO repos (name, path, target, ci_command, created_at)
		 VALUES (?, ?, ?, ?, datetime('now'))
		 ON CONFLICT(name) DO UPDATE SET
		   path = excluded.path, target = excluded.target, ci_command = excluded.ci_command`,
		name, path, target, ci)
	return err
}

// RemoveRepo deletes a repository; the bool reports whether it existed.
func (s *SQLiteStore) RemoveRepo(name string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM repos WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
