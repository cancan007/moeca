package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logDocker is a docker stub whose logs are readable only while the container
// "exists", so removing it reproduces the real failure mode.
type logDocker struct {
	fakeDocker
	logs map[string]string
}

func (d *logDocker) Logs(id string) (string, error) {
	if out, ok := d.logs[id]; ok {
		return out, nil
	}
	return "", errors.New("No such container")
}

func newLogServer(t *testing.T, d *logDocker) *Server {
	t.Helper()
	return &Server{cfg: &Config{LogDir: t.TempDir()}, docker: d, runs: map[string]*Run{}}
}

// The point of the archive: once the container is gone, `docker logs` fails and
// the stage's output would be lost. It must still be readable.
func TestStageLogSurvivesContainerRemoval(t *testing.T) {
	d := &logDocker{logs: map[string]string{"cid-1": "planner output\n"}}
	s := newLogServer(t, d)

	s.archiveStageLog("run-abc", "planner", "cid-1")

	// Container removed — this is what `docker rm` / prune does to the log.
	delete(d.logs, "cid-1")

	got, ok := s.stageLog("run-abc", "planner", "cid-1")
	if !ok {
		t.Fatal("stage log lost once the container was removed")
	}
	if got != "planner output\n" {
		t.Errorf("archived log = %q", got)
	}
}

// While the container is alive it stays the source of truth, so a running stage
// keeps streaming fresh output rather than a stale snapshot.
func TestStageLogPrefersLiveContainer(t *testing.T) {
	d := &logDocker{logs: map[string]string{"cid-1": "first\n"}}
	s := newLogServer(t, d)

	s.archiveStageLog("run-abc", "planner", "cid-1")
	d.logs["cid-1"] = "first\nsecond\n" // stage kept running after the archive

	got, _ := s.stageLog("run-abc", "planner", "cid-1")
	if got != "first\nsecond\n" {
		t.Errorf("stage log = %q, want the live container output", got)
	}
}

// A failed stage's log is the one you most need afterwards, so archiving must
// not depend on the exit status — archiveStageLog is called on every terminal
// state and only ever looks at the container.
func TestArchiveDoesNotDependOnOutcome(t *testing.T) {
	d := &logDocker{logs: map[string]string{"cid-fail": "boom: exit 1\n"}}
	s := newLogServer(t, d)

	s.archiveStageLog("run-abc", "tester", "cid-fail")
	delete(d.logs, "cid-fail")

	got, ok := s.stageLog("run-abc", "tester", "cid-fail")
	if !ok || !strings.Contains(got, "boom") {
		t.Errorf("failed stage log = %q (ok=%v), want it archived", got, ok)
	}
}

// Nothing to archive must be a no-op, not a stray file or a panic.
func TestArchiveIgnoresMissingContainer(t *testing.T) {
	d := &logDocker{logs: map[string]string{}}
	s := newLogServer(t, d)

	s.archiveStageLog("run-abc", "planner", "")     // stage never started
	s.archiveStageLog("run-abc", "planner", "gone") // container already removed

	if _, ok := s.stageLog("run-abc", "planner", ""); ok {
		t.Error("expected no archived log")
	}
}

// Ids reach us from request bodies and become path segments.
func TestStageLogPathStaysInsideLogDir(t *testing.T) {
	root := t.TempDir()
	s := &Server{cfg: &Config{LogDir: root}}

	for _, tc := range []struct{ runID, stageID string }{
		{"../../etc", "planner"},
		{"run-abc", "../../../etc/passwd"},
		{"run/../..", "st/../.."},
	} {
		got := s.stageLogPath(tc.runID, tc.stageID)
		rel, err := filepath.Rel(root, got)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("stageLogPath(%q, %q) = %q escapes %q", tc.runID, tc.stageID, got, root)
		}
	}
}

// The archive must not be world-readable: stage logs contain prompts and model
// output, i.e. whatever the task was working on.
func TestArchivedLogIsNotWorldReadable(t *testing.T) {
	d := &logDocker{logs: map[string]string{"cid-1": "secret-ish output"}}
	s := newLogServer(t, d)

	s.archiveStageLog("run-abc", "planner", "cid-1")

	info, err := os.Stat(s.stageLogPath("run-abc", "planner"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("archived log mode = %o, want no group/other access", perm)
	}
}
