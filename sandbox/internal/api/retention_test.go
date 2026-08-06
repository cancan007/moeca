package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// archiveAged creates a run archive directory whose mtime is `age` in the past.
func archiveAged(t *testing.T, root, runID string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(`{"id":"`+runID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(dir, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func intp(n int) *int { return &n }

func TestRetentionDefaultsToThirtyDays(t *testing.T) {
	s := New(&Config{LogDir: t.TempDir()})
	if got := s.retentionDays(); got != 30 {
		t.Errorf("retentionDays() = %d, want 30", got)
	}
}

// Zero must survive as "keep everything" rather than being read as "unset" and
// silently becoming 30 days.
func TestRetentionZeroMeansKeepEverything(t *testing.T) {
	root := t.TempDir()
	s := New(&Config{LogDir: root, LogRetentionDays: intp(0)})
	if got := s.retentionDays(); got != 0 {
		t.Fatalf("retentionDays() = %d, want 0", got)
	}
	old := archiveAged(t, root, "run-ancient", 365*24*time.Hour)
	if n := s.pruneArchives(time.Now()); n != 0 {
		t.Errorf("pruned %d archives with retention disabled", n)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("archive removed despite retention 0: %v", err)
	}
}

func TestPruneRemovesOnlyExpiredArchives(t *testing.T) {
	root := t.TempDir()
	s := New(&Config{LogDir: root, LogRetentionDays: intp(30)})

	expired := archiveAged(t, root, "run-old", 31*24*time.Hour)
	fresh := archiveAged(t, root, "run-new", 29*24*time.Hour)

	if n := s.pruneArchives(time.Now()); n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Error("expired archive was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("archive inside the window was removed: %v", err)
	}
}

// retention.json lives at the archive root next to the run directories; the
// sweeper walks that root, so it must not treat the state file as a run.
func TestPruneLeavesTheRetentionStateFile(t *testing.T) {
	root := t.TempDir()
	s := New(&Config{LogDir: root, LogRetentionDays: intp(1)})
	if err := s.setRetentionDays(1); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	_ = os.Chtimes(s.retentionPath(), old, old)

	s.pruneArchives(time.Now())

	if _, err := os.Stat(s.retentionPath()); err != nil {
		t.Errorf("retention state file was pruned: %v", err)
	}
}

// The runtime override is what Settings writes; it must win over the config
// default and survive a restart (a fresh Server over the same dir).
func TestRuntimeOverrideBeatsConfigAndPersists(t *testing.T) {
	root := t.TempDir()
	s := New(&Config{LogDir: root, LogRetentionDays: intp(30)})

	if err := s.setRetentionDays(7); err != nil {
		t.Fatal(err)
	}
	if got := s.retentionDays(); got != 7 {
		t.Errorf("retentionDays() = %d, want the override 7", got)
	}
	restarted := New(&Config{LogDir: root, LogRetentionDays: intp(30)})
	if got := restarted.retentionDays(); got != 7 {
		t.Errorf("after restart retentionDays() = %d, want 7", got)
	}
}

func TestRetentionEndpoints(t *testing.T) {
	root := t.TempDir()
	srv := newTest(&Config{Image: "img", LogDir: root}, &fakeDocker{})
	defer srv.Close()

	_, body := do(t, srv, "GET", "/retention", nil)
	if days, _ := body["days"].(float64); days != 30 {
		t.Errorf("GET days = %v, want 30", body["days"])
	}

	// Shortening the window applies immediately rather than at the next sweep.
	expired := archiveAged(t, root, "run-old", 10*24*time.Hour)
	if resp, _ := do(t, srv, "POST", "/retention", map[string]any{"days": 7}); resp.StatusCode != 200 {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Error("shortening retention did not prune immediately")
	}
	_, body = do(t, srv, "GET", "/retention", nil)
	if days, _ := body["days"].(float64); days != 7 {
		t.Errorf("GET after set = %v, want 7", body["days"])
	}

	// A negative window is meaningless; 0 is the way to say "keep everything".
	if resp, _ := do(t, srv, "POST", "/retention", map[string]any{"days": -1}); resp.StatusCode != 400 {
		t.Errorf("negative days status = %d, want 400", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "POST", "/retention", map[string]any{}); resp.StatusCode != 400 {
		t.Errorf("missing days status = %d, want 400", resp.StatusCode)
	}
}
