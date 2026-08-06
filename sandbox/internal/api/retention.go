package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Archive retention.
//
// Run archives are what remains of a finished run — its logs, and the stage
// commits that describe what each stage produced. Nothing deletes them, so
// without a policy they accumulate for the life of the install.
//
// Retention is expressed in days and applies to a run's whole archive directory,
// aged by its last modification. The default is 30 days: long enough that
// "what did that run do last month" is still answerable, short enough that the
// directory does not grow without bound. Zero means keep everything, which is a
// deliberate escape hatch rather than an accident — an operator who wants a
// permanent record can say so.
//
// The configured value is the default; an operator can change it at runtime from
// Settings. That override is persisted in the archive root rather than written
// back into the config file, which is shipped as a bundle resource and not ours
// to rewrite.

// DefaultRetentionDays is used when the config does not specify one.
const DefaultRetentionDays = 30

// pruneInterval is how often the sweeper runs. Retention is a housekeeping
// policy measured in days, so checking a few times a day is ample.
const pruneInterval = 6 * time.Hour

// retentionState is the persisted runtime override.
type retentionState struct {
	Days int `json:"days"`
}

func (s *Server) retentionPath() string {
	return filepath.Join(s.cfg.logDir(), "retention.json")
}

// retentionDays returns the effective retention: the runtime override if one has
// been set, else the configured default, else DefaultRetentionDays.
func (s *Server) retentionDays() int {
	if b, err := os.ReadFile(s.retentionPath()); err == nil {
		var st retentionState
		if json.Unmarshal(b, &st) == nil && st.Days >= 0 {
			return st.Days
		}
	}
	// A pointer so an explicit 0 in the config ("keep everything") is
	// distinguishable from the field being absent.
	if s.cfg.LogRetentionDays != nil && *s.cfg.LogRetentionDays >= 0 {
		return *s.cfg.LogRetentionDays
	}
	return DefaultRetentionDays
}

// setRetentionDays persists a runtime override. Negative values are rejected by
// the caller; zero is meaningful (keep everything).
func (s *Server) setRetentionDays(days int) error {
	if err := os.MkdirAll(s.cfg.logDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(retentionState{Days: days})
	if err != nil {
		return err
	}
	return os.WriteFile(s.retentionPath(), raw, 0o600)
}

// pruneArchives deletes run archives whose directory has not been touched within
// the retention window, and reports how many it removed. A retention of 0 keeps
// everything.
func (s *Server) pruneArchives(now time.Time) int {
	days := s.retentionDays()
	if days <= 0 {
		return 0
	}
	root := s.cfg.logDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("sandbox: pruning archives: %v", err)
		}
		return 0
	}
	cutoff := now.AddDate(0, 0, -days)
	removed := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue // retention.json and anything else at the root is not a run
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			log.Printf("sandbox: pruning %s: %v", e.Name(), err)
			continue
		}
		removed++
	}
	if removed > 0 {
		log.Printf("sandbox: pruned %d run archive(s) older than %d day(s)", removed, days)
	}
	return removed
}

// startArchivePruner sweeps once at startup — an install that was closed past
// the window should not have to wait for the first tick — and then periodically.
func (s *Server) startArchivePruner() {
	go func() {
		s.pruneArchives(time.Now())
		t := time.NewTicker(pruneInterval)
		defer t.Stop()
		for range t.C {
			s.pruneArchives(time.Now())
		}
	}()
}

func (s *Server) handleRetentionGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{
		"days":        s.retentionDays(),
		"defaultDays": DefaultRetentionDays,
	})
}

func (s *Server) handleRetentionSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Days *int `json:"days"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Days == nil {
		writeErr(w, 400, "days is required")
		return
	}
	if *req.Days < 0 {
		writeErr(w, 400, "days must be 0 (keep everything) or a positive number of days")
		return
	}
	if err := s.setRetentionDays(*req.Days); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Apply immediately: shortening the window should take effect now, not at
	// the next sweep.
	s.pruneArchives(time.Now())
	writeJSON(w, 200, map[string]any{"days": *req.Days, "defaultDays": DefaultRetentionDays})
}
