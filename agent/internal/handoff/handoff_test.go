package handoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteThenReadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	in := Manifest{Stage: "sup-plan", Run: "run-1", Task: "画像作成",
		Summary: "worker-0 draws the dog", Files: []string{"plan.md"}, StopReason: "end_turn"}
	if err := Write(dir, in); err != nil {
		t.Fatal(err)
	}
	got, ok := Read(dir, "sup-plan")
	if !ok {
		t.Fatal("manifest not found after writing it")
	}
	if got.Summary != in.Summary || got.StopReason != "end_turn" || len(got.Files) != 1 {
		t.Errorf("round-trip lost data: %+v", got)
	}
	if got.Time == "" {
		t.Error("Time was not stamped")
	}
}

// A stage with no id belongs to no run — writing under some placeholder name
// would put a manifest in the worktree that no dependent will ever ask for.
func TestWriteSkipsAnUnidentifiedStage(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Manifest{Summary: "hello"}); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, ".orchestra")); err == nil && len(entries) > 0 {
		t.Errorf("wrote %v for a stage with no id", entries)
	}
}

// The whole point: a dependency that produced nothing is reported as such. The
// run this was built for had a planner that answered in prose, and the worker
// saw only a failed read_file — which it read as "look harder", not "there is
// no plan".
func TestUpstreamReportsAStageThatLeftNothing(t *testing.T) {
	dir := t.TempDir()
	ms := Upstream(dir, []string{"sup-plan"})
	if len(ms) != 1 || ms[0].Stage != "sup-plan" {
		t.Fatalf("Upstream = %+v, want a placeholder for the missing stage", ms)
	}
	out := Render(ms)
	if !strings.Contains(out, "Reported nothing") {
		t.Errorf("rendered section does not say the stage produced nothing:\n%s", out)
	}
}

func TestComposeAppendsUpstreamToTheTask(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Manifest{Stage: "a", Summary: "the plan", Files: []string{"x.png"}}); err != nil {
		t.Fatal(err)
	}
	task := Compose("画像作成", Upstream(dir, []string{"a"}))
	for _, want := range []string{"画像作成", "the plan", "x.png"} {
		if !strings.Contains(task, want) {
			t.Errorf("composed task is missing %q:\n%s", want, task)
		}
	}
}

// No dependencies means the prompt is exactly what the caller wrote — a root
// stage must not pay for a section describing nothing.
func TestComposeLeavesARootStageAlone(t *testing.T) {
	if got := Compose("just this", nil); got != "just this" {
		t.Errorf("Compose = %q, want the task unchanged", got)
	}
}

// A stage id becomes a filename; it must not be able to point outside the
// manifest directory.
func TestPathCannotEscapeTheStagesDirectory(t *testing.T) {
	p := Path("/work", "../../etc/passwd")
	if !strings.HasPrefix(filepath.ToSlash(p), "/work/.orchestra/stages/") {
		t.Errorf("Path escaped: %s", p)
	}
}

func TestSummaryIsBounded(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, Manifest{Stage: "a", Summary: strings.Repeat("x", maxSummary*2)}); err != nil {
		t.Fatal(err)
	}
	got, _ := Read(dir, "a")
	if len(got.Summary) > maxSummary+32 {
		t.Errorf("summary is %d bytes, want it truncated near %d", len(got.Summary), maxSummary)
	}
}
