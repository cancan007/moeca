package prompt

import "testing"

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
