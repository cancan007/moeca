package api

import (
	"os"
	"path/filepath"
	"testing"

	"orchestra/sandbox/internal/docker"
)

// Shared mode puts every stage in one worktree, so concurrent stages would
// write over each other. It was only ever safe because callers happened to send
// maxParallel=1; that is now a stated precondition.
func TestSharedWorktreeRejectsParallelStages(t *testing.T) {
	srv := newTest(&Config{Image: "img"}, &fakeDocker{})
	defer srv.Close()

	_, code := startRun(t, srv, map[string]any{
		"taskId": "t", "worktreePath": t.TempDir(), "maxParallel": 2,
		"stages": []map[string]any{stage("a"), stage("b")},
	})
	if code != 400 {
		t.Fatalf("shared + maxParallel=2 status = %d, want 400", code)
	}

	// Serial shared runs stay fine.
	if _, code := startRun(t, srv, map[string]any{
		"taskId": "t2", "worktreePath": t.TempDir(), "maxParallel": 1,
		"stages": []map[string]any{stage("a")},
	}); code != 201 {
		t.Fatalf("shared + maxParallel=1 status = %d, want 201", code)
	}
}

// Shared mode records each stage as its own commit, so what a stage produced is
// a diffable boundary rather than an undifferentiated pile of edits.
func TestSharedWorktreeRecordsStageCommits(t *testing.T) {
	base := t.TempDir()
	gitInit(t, base)

	fake := &fakeDocker{}
	// Each stage "agent" writes its own file into the shared worktree.
	fake.createHook = func(spec docker.Spec) {
		stageID := spec.TaskID[len("t-"):]
		os.WriteFile(filepath.Join(spec.WorktreePath, stageID+".txt"), []byte(stageID+"\n"), 0o644)
	}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	id, code := startRun(t, srv, map[string]any{
		"taskId": "t", "worktreePath": base, "maxParallel": 1,
		"stages": []map[string]any{stage("plan"), stage("build", "plan")},
	})
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusDone {
		t.Fatalf("run status = %v, want done", run["status"])
	}

	byID := map[string]map[string]any{}
	for _, raw := range run["stages"].([]any) {
		st := raw.(map[string]any)
		byID[st["id"].(string)] = st
	}

	plan, build := byID["plan"], byID["build"]
	planSHA, _ := plan["commit"].(string)
	buildSHA, _ := build["commit"].(string)
	if planSHA == "" || buildSHA == "" {
		t.Fatalf("stages did not record commits: plan=%q build=%q", planSHA, buildSHA)
	}
	if planSHA == buildSHA {
		t.Error("both stages recorded the same commit; each stage needs its own boundary")
	}
	// The second stage builds on the first, so the history is per stage.
	if parent, _ := build["parent"].(string); parent != planSHA {
		t.Errorf("build.parent = %q, want plan's commit %q", parent, planSHA)
	}

	// Each stage reports only the files it touched — that is the artifact.
	files, _ := build["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("build files = %v, want just its own", files)
	}
	f := files[0].(map[string]any)
	if f["path"] != "build.txt" {
		t.Errorf("build touched %v, want build.txt", f["path"])
	}
	if add, _ := f["additions"].(float64); add != 1 {
		t.Errorf("build additions = %v, want 1", f["additions"])
	}
}

// A stage that changes nothing is a normal outcome, not a failure, and must not
// invent an empty commit.
func TestStageThatChangesNothingRecordsNoCommit(t *testing.T) {
	base := t.TempDir()
	gitInit(t, base)

	srv := newTest(&Config{Image: "img"}, &fakeDocker{}) // no createHook: writes nothing
	defer srv.Close()

	id, code := startRun(t, srv, map[string]any{
		"taskId": "t", "worktreePath": base, "maxParallel": 1,
		"stages": []map[string]any{stage("readonly")},
	})
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusDone {
		t.Fatalf("run status = %v, want done", run["status"])
	}
	st := run["stages"].([]any)[0].(map[string]any)
	if sha, ok := st["commit"].(string); ok && sha != "" {
		t.Errorf("read-only stage recorded commit %q; want none", sha)
	}
}

// Observed in real use: a worktree carrying leftovers from an earlier run made
// the first stage's `git add -A` sweep them up, so a planner that only read
// files was credited with 3 files and 111 added lines it had nothing to do with.
// Pre-existing changes belong to the run's baseline, not to a stage.
func TestPreExistingChangesAreNotAttributedToTheFirstStage(t *testing.T) {
	base := t.TempDir()
	gitInit(t, base)

	// Leftovers from "an earlier run", uncommitted when this run starts.
	if err := os.WriteFile(filepath.Join(base, "leftover.md"), []byte("from before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeDocker{}
	fake.createHook = func(spec docker.Spec) {
		stageID := spec.TaskID[len("t-"):]
		if stageID == "builder" {
			os.WriteFile(filepath.Join(spec.WorktreePath, "mine.txt"), []byte("mine\n"), 0o644)
		}
	}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	id, code := startRun(t, srv, map[string]any{
		"taskId": "t", "worktreePath": base, "maxParallel": 1,
		"stages": []map[string]any{stage("planner"), stage("builder", "planner")},
	})
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)

	byID := map[string]map[string]any{}
	for _, raw := range run["stages"].([]any) {
		st := raw.(map[string]any)
		byID[st["id"].(string)] = st
	}

	// The read-only planner produced nothing, so it gets no commit at all.
	if sha, ok := byID["planner"]["commit"].(string); ok && sha != "" {
		files, _ := byID["planner"]["files"].([]any)
		t.Errorf("planner was credited with commit %q and files %v; the leftovers are the run's baseline", sha, files)
	}
	// The builder reports only its own file.
	files, _ := byID["builder"]["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "mine.txt" {
		t.Errorf("builder files = %v, want just mine.txt", files)
	}
}
