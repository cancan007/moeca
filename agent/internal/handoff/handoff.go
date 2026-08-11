// Package handoff defines what one stage of a run leaves behind for the stages
// that depend on it.
//
// The transport was always standardised — every stage reads and writes the same
// /work worktree, and nothing else crosses the boundary. What a stage should
// LEAVE THERE was not: it lived as prose inside a prompt ("write the plan to
// .orchestra/plan.md"), a string that no code knew about, that nothing
// validated, and whose absence failed silently. A supervisor that answered in
// prose instead of writing the file produced a run where every stage exited 0
// and the worktree was empty.
//
// So the contract moves here. Every stage writes a manifest when it finishes —
// written by the runner, not by the model, so it cannot be forgotten — and
// every stage is handed its dependencies' manifests as part of its task, so it
// does not have to guess a filename to go looking for. A stage that reported
// nothing is then a visible fact rather than a missing file.
package handoff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Dir is where manifests live inside the worktree, one file per stage.
const Dir = ".orchestra/stages"

// maxSummary bounds how much of a stage's closing message is carried forward.
// The manifest is injected into every dependent's prompt, so an unbounded
// summary is an unbounded token bill on the next stage.
const maxSummary = 4000

// Manifest is one stage's account of itself.
type Manifest struct {
	Stage string `json:"stage"`
	Run   string `json:"run,omitempty"`
	Task  string `json:"task,omitempty"`
	// Summary is the agent's closing message — the text it ended its turn with.
	// Before this existed that text was discarded, which is how a planner's plan
	// could vanish between one stage and the next.
	Summary string `json:"summary,omitempty"`
	// Files are the worktree-relative paths this stage wrote, in the order the
	// tools reported them. Empty is meaningful: the stage produced nothing.
	Files []string `json:"files"`
	// StopReason is why the loop ended (end_turn, max_tokens, error…).
	StopReason string `json:"stopReason,omitempty"`
	Error      string `json:"error,omitempty"`
	Time       string `json:"time,omitempty"`
}

// Path is where a stage's manifest lives under workdir.
func Path(workdir, stage string) string {
	return filepath.Join(workdir, filepath.FromSlash(Dir), sanitize(stage)+".json")
}

// Write stores a manifest. A stage with no id has no identity to publish under
// (a bare agent run outside the orchestrator), so it writes nothing.
func Write(workdir string, m Manifest) error {
	if strings.TrimSpace(m.Stage) == "" {
		return nil
	}
	if m.Time == "" {
		m.Time = time.Now().UTC().Format(time.RFC3339)
	}
	if m.Files == nil {
		m.Files = []string{}
	}
	m.Summary = truncate(m.Summary, maxSummary)
	path := Path(workdir, m.Stage)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("handoff: %w", err)
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Read loads one stage's manifest. A missing file is reported as not-found so
// the caller can say "this stage reported nothing" rather than treat it as an
// error — that distinction is the whole point.
func Read(workdir, stage string) (Manifest, bool) {
	b, err := os.ReadFile(Path(workdir, stage))
	if err != nil {
		return Manifest{}, false
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, false
	}
	if m.Stage == "" {
		m.Stage = stage
	}
	return m, true
}

// Upstream reads the manifests of the given stages, in order.
func Upstream(workdir string, stages []string) []Manifest {
	out := make([]Manifest, 0, len(stages))
	for _, s := range stages {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if m, ok := Read(workdir, s); ok {
			out = append(out, m)
		} else {
			// Reported explicitly. A dependency that left nothing behind is the
			// single most useful thing a stage can know about its inputs, and
			// omitting it here is what made the failure look like an empty
			// worktree with no explanation.
			out = append(out, Manifest{Stage: s})
		}
	}
	return out
}

// Render turns upstream manifests into the section appended to a stage's task.
// Empty when there is nothing upstream, so a root stage's prompt is unchanged.
func Render(ms []Manifest) string {
	if len(ms) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Upstream stages\n\n")
	b.WriteString("These stages ran before you and handed their work to the shared worktree at /work. ")
	b.WriteString("This is what each of them reported; the files they list are already on disk.\n")
	for _, m := range ms {
		fmt.Fprintf(&b, "\n## %s\n", m.Stage)
		if m.Summary == "" && len(m.Files) == 0 {
			b.WriteString("Reported nothing and produced no files. Do not wait for its output — decide what to do without it.\n")
			continue
		}
		if m.Summary != "" {
			b.WriteString(m.Summary)
			if !strings.HasSuffix(m.Summary, "\n") {
				b.WriteString("\n")
			}
		}
		if len(m.Files) > 0 {
			fmt.Fprintf(&b, "\nFiles written: %s\n", strings.Join(m.Files, ", "))
		} else {
			b.WriteString("\nFiles written: none.\n")
		}
	}
	return b.String()
}

// Compose appends the upstream section to a task prompt.
func Compose(task string, ms []Manifest) string {
	section := Render(ms)
	if section == "" {
		return task
	}
	return strings.TrimRight(task, "\n") + "\n\n" + section
}

// sanitize keeps a stage id usable as a filename; ids come from a compiled
// template, but a manifest path must never escape the stages directory.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "stage"
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}

// SortedUnique returns paths in a stable order with duplicates removed — a file
// written three times is one artifact.
func SortedUnique(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = strings.TrimSpace(p); p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
