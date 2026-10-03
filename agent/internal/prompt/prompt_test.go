package prompt

import (
	"strings"
	"testing"
)

func TestBuildComposesFrame(t *testing.T) {
	out := Build(Env{
		Persona:    "You are a coding agent.",
		Workdir:    "/work",
		Provider:   "anthropic",
		Model:      "claude-opus-4-8",
		Compaction: true,
	})
	for _, want := range []string{
		"You are a coding agent.",
		"# Environment",
		"/work",
		"anthropic/claude-opus-4-8",
		"security gateway",
		"# Operating guidelines",
		"automatically summarized",
	} {
		if !contains(out, want) {
			t.Errorf("composed prompt missing %q\n---\n%s", want, out)
		}
	}
}

func TestBuildOmitsCompactionNoteWhenDisabled(t *testing.T) {
	out := Build(Env{Persona: "P", Compaction: false})
	if contains(out, "automatically summarized") {
		t.Error("compaction note should be absent when Compaction=false")
	}
	if !contains(out, "P") {
		t.Error("persona should lead the prompt")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// An agent with the search grant is told how the tool actually behaves.
//
// Two things it would otherwise get wrong. It reaches for a picture's address,
// because that is what a person would do, and gets nothing back every time: the
// action behind it reads pages for text and an image file has none. And when a
// search does return images, they go to the calling application as addresses and
// captions — never to the model — so an agent that is not told this reports
// having seen a diagram it only inferred from the text around it. One real run
// did each.
func TestBuildExplainsWebSearchWhenGranted(t *testing.T) {
	got := Build(Env{Persona: "You are an agent.", Workdir: "/work", WebSearch: true})

	for _, want := range []string{"# Web search", "cannot look at a picture", "not examine the image", "site:"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt does not mention %q:\n%s", want, got)
		}
	}
}

// An agent without the grant is told nothing about a tool it cannot call.
func TestBuildOmitsWebSearchWithoutTheGrant(t *testing.T) {
	got := Build(Env{Persona: "You are an agent.", Workdir: "/work"})
	if strings.Contains(got, "# Web search") {
		t.Errorf("prompt describes a tool this agent does not have:\n%s", got)
	}
}
