package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// A server tool is advertised by type, and must not carry the client-tool
// fields: the API rejects an input_schema on it.
func TestServerToolMarshalsWithoutClientFields(t *testing.T) {
	raw, err := json.Marshal(Tool{Name: "web_search", Type: WebSearchTool, MaxUses: 5})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)
	for _, want := range []string{`"type":"web_search_20260209"`, `"max_uses":5`} {
		if !strings.Contains(got, want) {
			t.Errorf("tool JSON missing %s: %s", want, got)
		}
	}
	for _, unwanted := range []string{"input_schema", "description", "allowed_domains", "blocked_domains"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("tool JSON should omit %s: %s", unwanted, got)
		}
	}
}

// Client tools keep their existing wire shape — the added omitempty tags must
// not have changed anything for them.
func TestClientToolWireShapeUnchanged(t *testing.T) {
	raw, _ := json.Marshal(Tool{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]any{"type": "object"},
	})
	got := string(raw)
	for _, want := range []string{`"name":"read_file"`, `"description":"Read a file"`, `"input_schema"`} {
		if !strings.Contains(got, want) {
			t.Errorf("client tool JSON missing %s: %s", want, got)
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := fields["type"]; ok {
		t.Errorf("client tool must not carry a server type: %s", got)
	}
}

// A stage pinned to an older model should search less well, not 400 — the same
// argument as the thinking downgrade next door.
func TestNormalizeDowngradesWebSearchOnLegacyModels(t *testing.T) {
	in := []Tool{
		{Name: "read_file", Description: "d", InputSchema: map[string]any{}},
		{Name: "web_search", Type: WebSearchTool, MaxUses: 5},
	}
	got := normalize(Request{Model: "claude-haiku-4-5-20251001", MaxTokens: 16000, Tools: in})

	if got.Tools[1].Type != webSearchToolLegacy {
		t.Errorf("web_search type = %q, want %q", got.Tools[1].Type, webSearchToolLegacy)
	}
	// The caller builds tool definitions once and reuses them every turn, so
	// normalize must not have patched the original.
	if in[1].Type != WebSearchTool {
		t.Errorf("normalize mutated the caller's tool slice: %q", in[1].Type)
	}
	if got := normalize(Request{Model: "claude-opus-4-8", Tools: in}); got.Tools[1].Type != WebSearchTool {
		t.Errorf("modern model: web_search type = %q, want unchanged", got.Tools[1].Type)
	}
}

// The OpenAI and Gemini dialects have no server tools. Declaring web_search as
// a function there would make the model call something nobody executes.
func TestOtherDialectsDropServerTools(t *testing.T) {
	req := Request{
		Model:     "gpt-4o",
		MaxTokens: 100,
		Messages:  []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		Tools: []Tool{
			{Name: "read_file", Description: "d", InputSchema: map[string]any{"type": "object"}},
			{Name: "web_search", Type: WebSearchTool, MaxUses: 5},
		},
	}

	oa := (&openAIClient{}).encode(req)
	if len(oa.Tools) != 1 || oa.Tools[0].Function.Name != "read_file" {
		t.Errorf("openai tools = %+v, want only read_file", oa.Tools)
	}

	gem := (&geminiClient{}).encode(req)
	if len(gem.Tools) != 1 || len(gem.Tools[0].FunctionDeclarations) != 1 ||
		gem.Tools[0].FunctionDeclarations[0].Name != "read_file" {
		t.Errorf("gemini tools = %+v, want only read_file", gem.Tools)
	}

	// A stage whose only tool is web_search must declare no functions at all,
	// rather than an empty declaration list the API would reject.
	only := Request{Model: "gemini-2.5-pro", Tools: []Tool{{Name: "web_search", Type: WebSearchTool}}}
	if got := (&geminiClient{}).encode(only); len(got.Tools) != 0 {
		t.Errorf("gemini tools = %+v, want none", got.Tools)
	}
}
