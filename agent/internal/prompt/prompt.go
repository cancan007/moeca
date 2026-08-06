// Package prompt composes the agent's system prompt from a persona plus a
// consistent operating frame (runtime environment + guidelines). Keeping the
// structure in one place — rather than a single flat string baked into main —
// lets every agent run share the same well-formed instructions while the
// persona stays overridable per task.
package prompt

import (
	"fmt"
	"strings"
)

// Env is the runtime context woven into the composed system prompt.
type Env struct {
	Persona    string // the base role/instructions (ORCHESTRA_SYSTEM or default)
	Workdir    string // the worktree the agent edits (e.g. /work)
	Provider   string // provider kind (anthropic/openai/gemini)
	Model      string // model id
	Compaction bool   // whether long histories are auto-summarized
}

// Build returns the composed system prompt. The persona leads; a fixed
// Environment and Operating-guidelines frame follows so behavior is consistent
// across providers. Fields left blank are omitted rather than rendered empty.
func Build(e Env) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(e.Persona))

	b.WriteString("\n\n# Environment\n")
	if e.Workdir != "" {
		fmt.Fprintf(&b, "- Working directory: %s (a git worktree; all edits happen here).\n", e.Workdir)
	}
	if e.Provider != "" || e.Model != "" {
		fmt.Fprintf(&b, "- Runtime: %s.\n", strings.TrimPrefix(e.Provider+"/"+e.Model, "/"))
	}
	b.WriteString("- All network access is mediated by the Orchestra security gateway; you hold no credentials.\n")

	b.WriteString("\n# Operating guidelines\n")
	b.WriteString("- Use the provided tools to inspect and edit files; make small, verifiable changes.\n")
	b.WriteString("- Read before you write, and confirm each tool result before the next step.\n")
	b.WriteString("- Reference files by relative path.\n")
	b.WriteString("- Stop as soon as the task is complete—do not perform unrequested work.\n")
	if e.Compaction {
		b.WriteString("- Long sessions are automatically summarized; treat any summary of earlier work as authoritative and keep progressing toward the goal.\n")
	}
	return b.String()
}
