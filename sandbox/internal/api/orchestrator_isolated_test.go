package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/sandbox/internal/docker"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init"},
		{"config", "commit.gpgsign", "false"},
		{"config", "user.email", "t@e.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644)
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
}

// TestRun_IsolatedWorktrees drives a plan → 2 workers → integrate DAG in
// isolated mode. Each stage "agent" (the fake) writes a stage-specific file into
// its OWN worktree; we assert the stages got DISTINCT worktrees (not the shared
// base), and that all outputs are merged back onto the base branch at the end.
func TestRun_IsolatedWorktrees(t *testing.T) {
	base := t.TempDir()
	gitInit(t, base)

	fake := &fakeDocker{}
	// Each stage writes "<stageID>.txt" into the worktree it was given.
	fake.createHook = func(spec docker.Spec) {
		stageID := strings.TrimPrefix(spec.TaskID, "t-")
		os.WriteFile(filepath.Join(spec.WorktreePath, stageID+".txt"), []byte(stageID+"\n"), 0o644)
	}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	body := map[string]any{
		"taskId":       "t",
		"worktreePath": base,
		"worktreeMode": "isolated",
		"maxParallel":  2,
		"stages": []map[string]any{
			stage("plan"),
			stage("worker-a", "plan"),
			stage("worker-b", "plan"),
			stage("integrate", "worker-a", "worker-b"),
		},
	}
	id, code := startRun(t, srv, body)
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusDone {
		t.Fatalf("run status = %v, want done", run["status"])
	}

	// each stage was handed its own worktree dir (distinct, none equal to base)
	fake.mu.Lock()
	dirs := map[string]string{}
	for _, spec := range fake.created {
		dirs[strings.TrimPrefix(spec.TaskID, "t-")] = spec.WorktreePath
	}
	fake.mu.Unlock()
	seen := map[string]bool{}
	for stg, dir := range dirs {
		if dir == base {
			t.Errorf("stage %s used the shared base worktree, want an isolated one", stg)
		}
		if seen[dir] {
			t.Errorf("stage %s reused another stage's worktree %s", stg, dir)
		}
		seen[dir] = true
	}

	// all stage outputs were integrated back onto the base branch
	for _, f := range []string{"plan.txt", "worker-a.txt", "worker-b.txt", "integrate.txt"} {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Errorf("base worktree missing %s after integrate", f)
		}
	}
}

// TestRun_IsolatedFallsBackWhenNotGit ensures a non-git base path degrades to
// shared mode instead of failing the run.
func TestRun_IsolatedFallsBackWhenNotGit(t *testing.T) {
	base := t.TempDir() // plain dir, not a git worktree
	fake := &fakeDocker{}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	body := map[string]any{
		"taskId":       "t",
		"worktreePath": base,
		"worktreeMode": "isolated",
		"stages":       []map[string]any{stage("only")},
	}
	id, code := startRun(t, srv, body)
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusDone {
		t.Errorf("run status = %v, want done (fallback to shared)", run["status"])
	}
	// the single stage mounted the shared base path
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 1 || fake.created[0].WorktreePath != base {
		t.Errorf("expected shared base worktree fallback, got %+v", fake.created)
	}
}
