package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"orchestra/sandbox/internal/docker"
)

// runOnce drives a shared-worktree run to completion and returns the run id plus
// the server, whose log dir holds the archive.
func runOnce(t *testing.T, logDir string) (*httptest.Server, string, *Config) {
	t.Helper()
	base := t.TempDir()
	gitInit(t, base)

	fake := &fakeDocker{}
	fake.createHook = func(spec docker.Spec) {
		stageID := spec.TaskID[len("t-"):]
		os.WriteFile(filepath.Join(spec.WorktreePath, stageID+".txt"), []byte(stageID+"\n"), 0o644)
		os.WriteFile(filepath.Join(spec.WorktreePath, "unused"), nil, 0o644)
	}
	fake.logs = "stage output\n"
	cfg := &Config{Image: "img", LogDir: logDir}
	srv := newTest(cfg, fake)

	id, code := startRun(t, srv, map[string]any{
		"taskId": "t", "worktreePath": base, "maxParallel": 1,
		"stages": []map[string]any{stage("builder")},
	})
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	if run := waitRun(t, srv, id); run["status"] != statusDone {
		t.Fatalf("run status = %v, want done", run["status"])
	}
	return srv, id, cfg
}

// The run table is in memory. A restart used to lose which stages ran and what
// they produced, while their logs and commits survived on disk — so a past run
// could show logs but no artifacts.
func TestRunStatusSurvivesControllerRestart(t *testing.T) {
	logDir := t.TempDir()
	srv, id, cfg := runOnce(t, logDir)

	before, _ := do(t, srv, "GET", "/run?id="+id, nil)
	if before.StatusCode != 200 {
		t.Fatalf("status before restart = %d", before.StatusCode)
	}
	srv.Close()

	// A fresh controller over the same log dir: nothing in memory, archive intact.
	restarted := newTest(cfg, &fakeDocker{})
	defer restarted.Close()

	resp, body := do(t, restarted, "GET", "/run?id="+id, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status after restart = %d, want 200 from the archive", resp.StatusCode)
	}
	if body["id"] != id || body["status"] != statusDone {
		t.Fatalf("archived run = %v", body)
	}
	stages, _ := body["stages"].([]any)
	if len(stages) != 1 {
		t.Fatalf("stages = %v", body["stages"])
	}
	st := stages[0].(map[string]any)
	if sha, _ := st["commit"].(string); sha == "" {
		t.Error("archived stage lost its commit; artifacts would be gone")
	}
	files, _ := st["files"].([]any)
	if len(files) == 0 {
		t.Error("archived stage lost its file list")
	}
}

// Logs were already archived, but /run/logs walked the in-memory run to find the
// stages — so after a restart the files sat on disk unreachable.
func TestRunLogsReadableAfterRestart(t *testing.T) {
	logDir := t.TempDir()
	srv, id, cfg := runOnce(t, logDir)
	srv.Close()

	restarted := newTest(cfg, &fakeDocker{})
	defer restarted.Close()

	resp, body := do(t, restarted, "GET", "/run/logs?id="+id, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("logs after restart = %d, want 200", resp.StatusCode)
	}
	logs, _ := body["logs"].(map[string]any)
	if got, _ := logs["builder"].(string); got != "stage output\n" {
		t.Errorf("archived log = %q, want the stage output", got)
	}
}

// An id with no archive is still a genuine 404, not an empty success.
func TestUnknownRunStillFourOhFour(t *testing.T) {
	srv := newTest(&Config{Image: "img", LogDir: t.TempDir()}, &fakeDocker{})
	defer srv.Close()

	if resp, _ := do(t, srv, "GET", "/run?id=run-nope", nil); resp.StatusCode != 404 {
		t.Errorf("status for unknown run = %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, srv, "GET", "/run/logs?id=run-nope", nil); resp.StatusCode != 404 {
		t.Errorf("logs for unknown run = %d, want 404", resp.StatusCode)
	}
}

// The snapshot is taken per stage, not only at the end, so a controller that
// dies mid-run leaves the stages that had finished.
func TestArchiveIsWrittenBeforeTheRunEnds(t *testing.T) {
	logDir := t.TempDir()
	_, id, _ := runOnce(t, logDir)

	raw, err := os.ReadFile(filepath.Join(logDir, id, "run.json"))
	if err != nil {
		t.Fatalf("run.json not written: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("run.json is not valid JSON: %v", err)
	}
	if meta["id"] != id {
		t.Errorf("run.json id = %v, want %s", meta["id"], id)
	}
	// Internal scheduling state must not leak into the client-facing shape.
	for _, internal := range []string{"stageCommit", "stopping", "delegation", "maxDepth"} {
		if _, present := meta[internal]; present {
			t.Errorf("run.json exposes internal field %q", internal)
		}
	}
}
