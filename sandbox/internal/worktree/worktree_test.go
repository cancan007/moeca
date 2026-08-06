package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// baseRepo creates a git repo on main with one committed file and returns it.
func baseRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "-c", "init.defaultBranch=main", "init")
	git(t, dir, "config", "commit.gpgsign", "false")
	git(t, dir, "config", "user.email", "t@e.com")
	git(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "init")
	return dir
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSupervisorShapeIsolatesThenIntegrates mirrors a plan → 2 parallel workers
// → integrate DAG. Each worker edits a DIFFERENT file in its OWN worktree; the
// integrate stage merges both and its commit lands back on the base branch.
func TestSupervisorShapeIsolatesThenIntegrates(t *testing.T) {
	base := baseRepo(t)
	m, err := New(base, "run-1")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Cleanup()

	// plan stage (no deps): writes the plan
	planDir, err := m.Prepare("plan", nil)
	if err != nil {
		t.Fatalf("Prepare plan: %v", err)
	}
	write(t, planDir, "plan.md", "do A and B\n")
	planSHA, err := m.Commit("plan")
	if err != nil {
		t.Fatalf("Commit plan: %v", err)
	}

	// two workers, each seeded from the plan, editing different files in isolation
	wADir, err := m.Prepare("worker-a", []string{planSHA})
	if err != nil {
		t.Fatalf("Prepare worker-a: %v", err)
	}
	wBDir, err := m.Prepare("worker-b", []string{planSHA})
	if err != nil {
		t.Fatalf("Prepare worker-b: %v", err)
	}
	// isolation: worker-a cannot see worker-b's file and vice-versa
	if _, err := os.Stat(filepath.Join(wADir, "b.txt")); err == nil {
		t.Error("worker-a worktree leaked worker-b's file")
	}
	write(t, wADir, "a.txt", "from A\n")
	write(t, wBDir, "b.txt", "from B\n")
	// both must see the plan they were seeded from
	if _, err := os.Stat(filepath.Join(wADir, "plan.md")); err != nil {
		t.Error("worker-a missing seeded plan.md")
	}
	aSHA, _ := m.Commit("worker-a")
	bSHA, _ := m.Commit("worker-b")

	// integrate depends on both workers -> its worktree merges A and B together
	intDir, err := m.Prepare("integrate", []string{aSHA, bSHA})
	if err != nil {
		t.Fatalf("Prepare integrate: %v", err)
	}
	for _, f := range []string{"a.txt", "b.txt", "plan.md"} {
		if _, err := os.Stat(filepath.Join(intDir, f)); err != nil {
			t.Errorf("integrate worktree missing %s (merge of workers failed)", f)
		}
	}
	write(t, intDir, "RESULT.md", "integrated\n")
	intSHA, _ := m.Commit("integrate")

	// land the sink on the base branch
	if err := m.Integrate([]string{intSHA}); err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	// base worktree now contains everything
	for _, f := range []string{"a.txt", "b.txt", "plan.md", "RESULT.md"} {
		if _, err := os.Stat(filepath.Join(base, f)); err != nil {
			t.Errorf("base worktree missing %s after integrate", f)
		}
	}
}

// TestPrepareConflictIsReported ensures a genuine merge conflict surfaces as an
// error instead of a corrupt worktree.
func TestPrepareConflictIsReported(t *testing.T) {
	base := baseRepo(t)
	m, err := New(base, "run-2")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Cleanup()

	// two sibling stages edit the SAME line differently
	aDir, _ := m.Prepare("a", nil)
	bDir, _ := m.Prepare("b", nil)
	write(t, aDir, "conflict.txt", "A wins\n")
	write(t, bDir, "conflict.txt", "B wins\n")
	aSHA, _ := m.Commit("a")
	bSHA, _ := m.Commit("b")

	// a stage depending on both must fail to merge them
	if _, err := m.Prepare("merge", []string{aSHA, bSHA}); err == nil {
		t.Fatal("expected a merge conflict error, got nil")
	}
	// the failed stage left no half-made worktree registered
	if d := m.StageDir("merge"); d != "" {
		t.Errorf("conflicted stage should not retain a worktree, got %q", d)
	}
}

// TestCommitNoChangesReturnsHead verifies a stage that changes nothing still
// yields a valid parent commit for its dependents.
func TestCommitNoChangesReturnsHead(t *testing.T) {
	base := baseRepo(t)
	m, err := New(base, "run-3")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Cleanup()
	if _, err := m.Prepare("noop", nil); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	sha, err := m.Commit("noop")
	if err != nil || sha == "" {
		t.Fatalf("Commit no-op: sha=%q err=%v", sha, err)
	}
}
