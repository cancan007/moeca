package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A run reports what it produced, not just that its stages exited 0. The two
// were conflated before: every stage could succeed and leave the output
// directory empty, and the run still came back `done` with nothing to say.

func writeManifest(t *testing.T, work, stage string, files []string) {
	t.Helper()
	dir := filepath.Join(work, ".orchestra", "stages")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"stage": stage, "files": files})
	if err := os.WriteFile(filepath.Join(dir, stage+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectArtifactsUnionsEveryStage(t *testing.T) {
	work := t.TempDir()
	writeManifest(t, work, "plan", nil)
	writeManifest(t, work, "draw", []string{"art/dog.png"})
	writeManifest(t, work, "narrate", []string{"art/voice.mp3", "art/dog.png"})

	got := collectArtifacts(work)
	// Deduplicated and sorted: a file written twice is one artifact.
	if len(got) != 2 || got[0] != "art/dog.png" || got[1] != "art/voice.mp3" {
		t.Errorf("artifacts = %v", got)
	}
}

// The distinction the whole change exists for: stages that ran and reported
// nothing are not the same as stages that produced something.
func TestCollectArtifactsIsEmptyWhenNothingWasWritten(t *testing.T) {
	work := t.TempDir()
	writeManifest(t, work, "plan", nil)
	writeManifest(t, work, "integrate", []string{})
	if got := collectArtifacts(work); len(got) != 0 {
		t.Errorf("artifacts = %v, want none", got)
	}
}

// The manifests are the run describing itself, not something it was asked to
// make — counting them would make every run look productive.
func TestManifestsAreNotCountedAsArtifacts(t *testing.T) {
	work := t.TempDir()
	writeManifest(t, work, "plan", nil)
	for _, a := range collectArtifacts(work) {
		if filepath.Ext(a) == ".json" && filepath.Dir(a) == ".orchestra/stages" {
			t.Errorf("a manifest was counted as an artifact: %s", a)
		}
	}
}

// A worktree with no manifests at all (an older agent, a stage that never
// started) reports nothing rather than erroring.
func TestCollectArtifactsToleratesAMissingDirectory(t *testing.T) {
	if got := collectArtifacts(t.TempDir()); got != nil && len(got) != 0 {
		t.Errorf("artifacts = %v, want none", got)
	}
}
