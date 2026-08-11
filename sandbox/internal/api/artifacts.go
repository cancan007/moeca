package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// What a run actually produced.
//
// "Every stage exited 0" and "the run made something" are different facts, and
// reporting only the first is how a run that generated nothing came back as
// `done` with an empty output directory and nothing to explain it. The stages
// already say what they wrote — the runner records it from the tools rather
// than from what the model claims — so the run can carry the same fact up.
//
// The manifests are read off the worktree rather than tracked in memory
// because sub-agents write them too: a delegated stage's output belongs to the
// run just as much as its parent's.

// handoffStagesDir mirrors agent/internal/handoff.Dir. The two services share
// the worktree, not a package.
const handoffStagesDir = ".orchestra/stages"

// stageManifest is the part of a stage's handoff manifest this service reads.
type stageManifest struct {
	Stage string   `json:"stage"`
	Files []string `json:"files"`
}

// collectArtifacts returns the worktree-relative paths a run's stages wrote,
// deduplicated and sorted. A run with none produced nothing, whatever its
// stages' exit codes were.
//
// The manifests themselves are deliberately not counted: they are the run
// describing itself, not something it was asked to make.
func collectArtifacts(worktree string) []string {
	dir := filepath.Join(worktree, filepath.FromSlash(handoffStagesDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m stageManifest
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, f := range m.Files {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}
