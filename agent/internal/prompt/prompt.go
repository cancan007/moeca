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
	// WebSearch reports whether this run was granted the provider-side search
	// tool. It changes the prompt because the tool does not behave the way an
	// agent assumes it does — see the section Build writes for it.
	WebSearch bool
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
	if e.WebSearch {
		b.WriteString(webSearchGuidance)
	}
	return b.String()
}

// webSearchGuidance tells an agent how the search tool actually behaves.
//
// The second line is the reason this section exists: the tool cannot show the
// agent a picture, and nothing about using it says so. A search asked for images
// returns `image_result` entries — an address, the page it came from, a
// thumbnail address, sometimes a caption — which the provider's own
// documentation describes as metadata for the calling application to render,
// "returned separately from the assistant message". Opening an image address
// instead fails outright, because the action behind it reads pages for text and
// a .png has none.
//
// So an agent asked to look at a diagram has no way to do it, and two real runs
// showed both ways that goes wrong: one spent six of its eight searches on
// picture addresses before giving up, and one reported having seen a diagram it
// could only have inferred from the surrounding text. The second is the worse
// failure, which is why the line ends by saying what to report instead.
//
// Written as behaviour rather than as a rule, because the model has to choose
// the right action for a goal, not obey a prohibition.
const webSearchGuidance = `
# Web search
- The model provider runs the search; you never fetch anything yourself, and the results arrive inside the same reply.
- You cannot look at a picture. Image results reach the application that displays them, not you: you get an address, a caption and the page it came from, and never the picture itself. Opening an image address returns nothing at all — the page reader behind that action looks for text, and an image file has none. So a question that only looking at the image could answer cannot be answered here: say what the surrounding text says, give the address, and state plainly that you did not examine the image. Never describe a picture you have not been shown.
- A search is still the way to find where something lives; ` + "`site:`" + ` narrows one to a single repository or domain.
- Searches are capped and billed per use, and the tool is withdrawn once the grant is spent. Spend them on queries that can answer the question rather than on addresses you already have.
`
