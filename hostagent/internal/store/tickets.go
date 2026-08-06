package store

import (
	"database/sql"
	"encoding/json"

	"orchestra/hostagent/internal/tasksource"
)

// UpsertTickets inserts or updates pulled tickets by id. Labels and the
// source-native payload are stored as JSON.
func (s *SQLiteStore) UpsertTickets(ts []tasksource.Ticket) error {
	for _, t := range ts {
		labels, err := json.Marshal(t.Labels)
		if err != nil {
			return err
		}
		if len(labels) == 0 {
			labels = []byte("[]")
		}
		raw := t.Raw
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		if _, err := s.db.Exec(
			`INSERT INTO pulled_tickets
			   (id, source, title, body, url, state, repo, branch, labels, raw, updated_at, pulled_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
			 ON CONFLICT(id) DO UPDATE SET
			   title=excluded.title, body=excluded.body, url=excluded.url, state=excluded.state,
			   repo=excluded.repo, branch=excluded.branch, labels=excluded.labels, raw=excluded.raw,
			   updated_at=excluded.updated_at, pulled_at=excluded.pulled_at`,
			t.ID, t.Source, t.Title, t.Body, t.URL, t.State, t.Repo, t.Branch,
			string(labels), string(raw), t.UpdatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

// TicketByID returns a single stored ticket (nil if not found).
func (s *SQLiteStore) TicketByID(id string) (*tasksource.Ticket, error) {
	var t tasksource.Ticket
	var labels, raw string
	err := s.db.QueryRow(
		`SELECT id, source, title, body, url, state, repo, branch, labels, raw, updated_at FROM pulled_tickets WHERE id = ?`, id,
	).Scan(&t.ID, &t.Source, &t.Title, &t.Body, &t.URL, &t.State, &t.Repo, &t.Branch, &labels, &raw, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(labels), &t.Labels)
	t.Raw = json.RawMessage(raw)
	return &t, nil
}

// Tickets returns stored tickets, optionally filtered by source, newest first.
func (s *SQLiteStore) Tickets(source string) ([]tasksource.Ticket, error) {
	return s.tickets("source = ?", source)
}

// TicketsExcluding returns stored tickets whose source is NOT the given one
// (used to keep the Delivery-only "github" source out of the Daily list).
func (s *SQLiteStore) TicketsExcluding(source string) ([]tasksource.Ticket, error) {
	return s.tickets("source <> ?", source)
}

func (s *SQLiteStore) tickets(cond, source string) ([]tasksource.Ticket, error) {
	q := `SELECT id, source, title, body, url, state, repo, branch, labels, raw, updated_at FROM pulled_tickets`
	var args []any
	if source != "" {
		q += ` WHERE ` + cond
		args = append(args, source)
	}
	q += ` ORDER BY updated_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tasksource.Ticket
	for rows.Next() {
		var t tasksource.Ticket
		var labels, raw string
		if err := rows.Scan(&t.ID, &t.Source, &t.Title, &t.Body, &t.URL, &t.State,
			&t.Repo, &t.Branch, &labels, &raw, &t.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &t.Labels)
		t.Raw = json.RawMessage(raw)
		out = append(out, t)
	}
	return out, rows.Err()
}
