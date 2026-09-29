package main

import (
	"testing"

	"orchestra/agent/internal/llm"
)

// The grant has to survive the whole way from the stage to the tool registry.
//
// This is the step that broke: the compiler put `web` on the stage, the
// controller forwarded ORCHESTRA_WEB_SEARCH, the encoder knew how to translate
// it — and this decision still said Anthropic, so on an OpenAI model the tool
// was never registered. Nothing errored; the model simply never saw a web search
// tool and reached for the knowledge base instead.
func TestWebSearchGrantFollowsTheDialect(t *testing.T) {
	cases := []struct {
		provider string
		want     bool
	}{
		{llm.KindAnthropic, true},
		{llm.KindOpenAIResponses, true},
		{"", true}, // unset means Anthropic
		{llm.KindOpenAI, false},
		{llm.KindGemini, false},
	}
	for _, c := range cases {
		_, on, err := webSearchGrant("1", c.provider)
		if err != nil {
			t.Fatalf("provider %q: %v", c.provider, err)
		}
		if on != c.want {
			t.Errorf("webSearchGrant(on, %q) = %v, want %v", c.provider, on, c.want)
		}
	}
}

// The cap and the domain list have to survive the parse, not just the switch.
func TestWebSearchGrantKeepsItsTerms(t *testing.T) {
	cfg, on, err := webSearchGrant(`{"maxUses":3,"allowedDomains":["go.dev"]}`, llm.KindOpenAIResponses)
	if err != nil || !on {
		t.Fatalf("grant = %v, %v", on, err)
	}
	if cfg.MaxUses != 3 {
		t.Errorf("MaxUses = %d, want 3", cfg.MaxUses)
	}
	if len(cfg.AllowedDomains) != 1 || cfg.AllowedDomains[0] != "go.dev" {
		t.Errorf("AllowedDomains = %v, want [go.dev]", cfg.AllowedDomains)
	}
}

// An absent variable is not a grant, and an explicit off is not an error.
func TestWebSearchGrantOffAndAbsent(t *testing.T) {
	if _, on, err := webSearchGrant("", llm.KindAnthropic); on || err != nil {
		t.Errorf("absent grant = %v, %v; want off and no error", on, err)
	}
	if _, on, err := webSearchGrant("0", llm.KindAnthropic); on || err != nil {
		t.Errorf("explicit off = %v, %v; want off and no error", on, err)
	}
	if _, _, err := webSearchGrant("{not json", llm.KindAnthropic); err == nil {
		t.Error("a malformed grant was accepted")
	}
}
