package gateway

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sync"

	_ "modernc.org/sqlite"
)

// AuditStore is the durable, append-only audit plane. Each access record is
// stored with a SHA-256 hash chain (hash = SHA256(prevHash + recordJSON)), so
// any later edit or deletion breaks the chain — turning the gateway's
// structural tamper-evidence (capture at the chokepoint) into a cryptographic
// one that /audit/verify can check. Backed by embedded SQLite (pure-Go driver).
type AuditStore struct {
	db       *sql.DB
	mu       sync.Mutex
	lastHash string
}

// OpenAuditStore opens (and creates) the audit database at path.
func OpenAuditStore(path string) (*AuditStore, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS audit_log (
		seq       INTEGER PRIMARY KEY AUTOINCREMENT,
		time      TEXT NOT NULL,
		service   TEXT NOT NULL DEFAULT '',
		status    INTEGER NOT NULL DEFAULT 0,
		session   TEXT NOT NULL DEFAULT '',
		record    TEXT NOT NULL,
		prev_hash TEXT NOT NULL,
		hash      TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	s := &AuditStore{db: db}
	// resume the chain from the last stored hash
	if err := db.QueryRow(`SELECT hash FROM audit_log ORDER BY seq DESC LIMIT 1`).Scan(&s.lastHash); err != nil && err != sql.ErrNoRows {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *AuditStore) Close() error { return s.db.Close() }

// append records one access log, extending the hash chain. It is serialised so
// the chain is well-ordered under concurrent requests.
func (s *AuditStore) append(rec accessLog) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sum := sha256.Sum256(append([]byte(s.lastHash), b...))
	h := hex.EncodeToString(sum[:])
	if _, err := s.db.Exec(
		`INSERT INTO audit_log (time, service, status, session, record, prev_hash, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.Time, rec.Service, rec.Status, rec.Session, string(b), s.lastHash, h,
	); err != nil {
		return err
	}
	s.lastHash = h
	return nil
}

// recent returns up to limit records, newest first.
func (s *AuditStore) recent(limit int) ([]accessLog, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT record FROM audit_log ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []accessLog
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rec accessLog
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// verifyResult reports the outcome of a chain integrity check.
type verifyResult struct {
	OK        bool  `json:"ok"`
	Count     int64 `json:"count"`
	BrokenSeq int64 `json:"brokenSeq,omitempty"` // first tampered row (0 if intact)
}

// verify recomputes the hash chain over every row and reports the first break.
func (s *AuditStore) verify() (verifyResult, error) {
	rows, err := s.db.Query(`SELECT seq, record, prev_hash, hash FROM audit_log ORDER BY seq ASC`)
	if err != nil {
		return verifyResult{}, err
	}
	defer rows.Close()
	prev := ""
	var count int64
	for rows.Next() {
		var seq int64
		var record, prevHash, hash string
		if err := rows.Scan(&seq, &record, &prevHash, &hash); err != nil {
			return verifyResult{}, err
		}
		count++
		sum := sha256.Sum256(append([]byte(prev), []byte(record)...))
		want := hex.EncodeToString(sum[:])
		if prevHash != prev || hash != want {
			return verifyResult{OK: false, Count: count, BrokenSeq: seq}, rows.Err()
		}
		prev = hash
	}
	return verifyResult{OK: true, Count: count}, rows.Err()
}
