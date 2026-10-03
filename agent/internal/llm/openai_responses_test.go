package llm

import (
	"encoding/json"
	"testing"
)

// The Responses dialect exists for one reason — a reasoning model with tools —
// so these tests cover the parts that break that: the item shapes, the
// round-trip of reasoning state, and the two policy decisions encoded in the
// request (store off, server tools dropped).

func orEncoded(t *testing.T, req Request) map[string]any {
	t.Helper()
	c := &openAIResponsesClient{}
	raw, err := json.Marshal(c.encode(req))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func orItems(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, ok := m["input"].([]any)
	if !ok {
		t.Fatalf("input was not a list: %#v", m["input"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		item, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("input item was not an object: %#v", it)
		}
		out = append(out, item)
	}
	return out
}

// Astra rejects effort "none" and rejects tools on /v1/chat/completions, so the
// only working combination is this endpoint with a real effort level.
func TestResponsesSendsEffortToReasoningModels(t *testing.T) {
	m := orEncoded(t, Request{
		Model:        "gpt-6-astra",
		MaxTokens:    100,
		OutputConfig: &OutputConfig{Effort: "high"},
		Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
	})
	r, ok := m["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("no reasoning object: %#v", m["reasoning"])
	}
	if r["effort"] != "high" {
		t.Errorf("effort = %v, want high", r["effort"])
	}
	if m["max_output_tokens"] != float64(100) {
		t.Errorf("max_output_tokens = %v, want 100", m["max_output_tokens"])
	}
}

// xhigh and max are the agent's top two levels. Chat Completions has no such
// values and oaEffort clamps them to high; this endpoint takes them, so
// clamping here would quietly cap the most expensive stages.
func TestResponsesDoesNotClampHighEfforts(t *testing.T) {
	for _, effort := range []string{"xhigh", "max"} {
		m := orEncoded(t, Request{
			Model:        "gpt-6-astra",
			MaxTokens:    100,
			OutputConfig: &OutputConfig{Effort: effort},
			Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		})
		r, _ := m["reasoning"].(map[string]any)
		if r == nil || r["effort"] != effort {
			t.Errorf("effort %q was not passed through: %#v", effort, m["reasoning"])
		}
	}
}

// A chat model answers an unexpected reasoning argument with a 400, exactly as
// it does for reasoning_effort on the other endpoint.
func TestResponsesOmitsReasoningForChatModels(t *testing.T) {
	m := orEncoded(t, Request{
		Model:        "gpt-4o",
		MaxTokens:    100,
		OutputConfig: &OutputConfig{Effort: "medium"},
		Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
	})
	if _, present := m["reasoning"]; present {
		t.Error("gpt-4o was sent a reasoning object; it rejects the whole request")
	}
	if _, present := m["include"]; present {
		t.Error("gpt-4o was asked for encrypted reasoning it does not produce")
	}
}

// store:false is the value we mean to send, not a field we forget: omitting it
// opts into the API default, which is to keep the conversation provider-side.
func TestResponsesKeepsStateClientSide(t *testing.T) {
	m := orEncoded(t, Request{
		Model:        "gpt-6-astra",
		MaxTokens:    100,
		OutputConfig: &OutputConfig{Effort: "medium"},
		Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
	})
	store, present := m["store"]
	if !present {
		t.Fatal("store was omitted; the API default keeps the conversation")
	}
	if store != false {
		t.Errorf("store = %v, want false", store)
	}
	inc, _ := m["include"].([]any)
	if len(inc) != 1 || inc[0] != "reasoning.encrypted_content" {
		t.Errorf("include = %#v, want [reasoning.encrypted_content]", m["include"])
	}
}

// A tool call and its answer correlate by call_id, and the answer is a
// top-level item rather than a message with a "tool" role.
func TestResponsesToolRoundTrip(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		Messages: []Message{
			{Role: "user", Content: []Block{TextBlock("read the file")}},
			{Role: "assistant", Content: []Block{
				{Type: "reasoning", Raw: json.RawMessage(`{"type":"reasoning","encrypted_content":"ENC"}`)},
				{Type: BlockToolUse, ID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"a.txt"}`)},
			}},
			{Role: "user", Content: []Block{ToolResultBlock("call_1", "hello", false)}},
		},
		Tools: []Tool{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	})

	items := orItems(t, m)
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4: %#v", len(items), items)
	}
	// The reasoning item must survive byte-for-byte and must still precede the
	// call it justifies: the API rejects one that arrives without its item.
	if items[1]["type"] != "reasoning" || items[1]["encrypted_content"] != "ENC" {
		t.Errorf("reasoning item did not round-trip: %#v", items[1])
	}
	if items[2]["type"] != "function_call" || items[2]["call_id"] != "call_1" {
		t.Errorf("function_call item wrong: %#v", items[2])
	}
	if items[3]["type"] != "function_call_output" || items[3]["call_id"] != "call_1" || items[3]["output"] != "hello" {
		t.Errorf("function_call_output item wrong: %#v", items[3])
	}

	// Tools are flat here — name and parameters sit on the tool, not under a
	// nested "function" object as in Chat Completions.
	tools, _ := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "read" {
		t.Errorf("tool shape wrong: %#v", tool)
	}
	if _, nested := tool["function"]; nested {
		t.Error("tool nested its definition under \"function\"; that is the Chat Completions shape")
	}
}

// A no-argument call may come back with empty arguments, and "" is not
// parseable JSON on the way in.
func TestResponsesEmptyToolArgumentsBecomeAnObject(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		Messages: []Message{
			{Role: "assistant", Content: []Block{{Type: BlockToolUse, ID: "c", Name: "ls"}}},
		},
	})
	items := orItems(t, m)
	if items[0]["arguments"] != "{}" {
		t.Errorf("arguments = %#v, want {}", items[0]["arguments"])
	}
}

// The web_search grant is translated: this API has the same built-in tool, and a
// built-in is named by its type alone — no name, description or parameters.
func TestResponsesTranslatesWebSearch(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		Messages:  []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		Tools:     []Tool{{Name: "web_search", Type: WebSearchTool, MaxUses: 5}},
	})
	tools, _ := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v, want the search grant", m["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Errorf("type = %#v, want web_search", tool["type"])
	}
	if _, named := tool["name"]; named {
		t.Errorf("built-in tool carries a name: %#v", tool)
	}
	// max_uses has no field in this API. It is deliberately absent rather than
	// approximated, and the agent enforces the cap by counting what comes back —
	// see the loop. A test that accepted some invented field here would be
	// asserting a request the API rejects.
	if _, capped := tool["max_uses"]; capped {
		t.Errorf("invented a max_uses field: %#v", tool)
	}
}

// Domain restriction does cross, and nests under one object rather than the two
// mutually exclusive fields the Anthropic dialect takes.
func TestResponsesWebSearchDomainFilters(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		Messages:  []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		Tools:     []Tool{{Name: "web_search", Type: WebSearchTool, MaxUses: 5, AllowedDomains: []string{"go.dev"}}},
	})
	tools, _ := m["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	filters, ok := tool["filters"].(map[string]any)
	if !ok {
		t.Fatalf("no filters on the grant: %#v", tool)
	}
	allowed, _ := filters["allowed_domains"].([]any)
	if len(allowed) != 1 || allowed[0] != "go.dev" {
		t.Errorf("allowed_domains = %#v, want [go.dev]", filters["allowed_domains"])
	}
	if _, blocked := filters["blocked_domains"]; blocked {
		t.Errorf("sent both domain lists: %#v", filters)
	}
}

// A grant with no domain restriction carries no filters object at all, rather
// than an empty one.
func TestResponsesWebSearchWithoutFilters(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		Messages:  []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		Tools:     []Tool{{Name: "web_search", Type: WebSearchTool, MaxUses: 5}},
	})
	tools, _ := m["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	if _, present := tool["filters"]; present {
		t.Errorf("empty filters were sent: %#v", tool)
	}
}

// The system prompt is "instructions" in this dialect, and an image rides in a
// content part list rather than a message of its own.
func TestResponsesSystemAndImage(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		System:    "be terse",
		Messages: []Message{{Role: "user", Content: []Block{
			TextBlock("what is this"), ImageBlock("image/png", "AAA"),
		}}},
	})
	if m["instructions"] != "be terse" {
		t.Errorf("instructions = %#v", m["instructions"])
	}
	items := orItems(t, m)
	parts, _ := items[0]["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("got %d content parts, want 2: %#v", len(parts), items[0]["content"])
	}
	first, _ := parts[0].(map[string]any)
	second, _ := parts[1].(map[string]any)
	if first["type"] != "input_text" {
		t.Errorf("text part type = %#v, want input_text", first["type"])
	}
	if second["type"] != "input_image" || second["image_url"] != "data:image/png;base64,AAA" {
		t.Errorf("image part wrong: %#v", second)
	}
}

/* ─── decode ─── */

func TestResponsesDecode(t *testing.T) {
	body := []byte(`{
	  "id": "resp_1", "model": "gpt-6-astra", "status": "completed",
	  "output": [
	    {"type":"reasoning","id":"rs_1","encrypted_content":"ENC","summary":[]},
	    {"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"working on it"}]},
	    {"type":"function_call","id":"fc_1","call_id":"call_9","name":"read","arguments":"{\"path\":\"a.txt\"}"}
	  ],
	  "usage": {"input_tokens": 11, "output_tokens": 22}
	}`)
	var out orResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}

	var content []Block
	for _, item := range out.Output {
		if b, ok := decodeItem(item); ok {
			content = append(content, b)
		}
	}
	if len(content) != 3 {
		t.Fatalf("got %d blocks, want 3: %#v", len(content), content)
	}
	// Reasoning stays unmodelled so it can be handed back verbatim.
	if content[0].Type != "reasoning" || len(content[0].Raw) == 0 {
		t.Errorf("reasoning block lost its raw payload: %#v", content[0])
	}
	if content[1].Type != BlockText || content[1].Text != "working on it" {
		t.Errorf("text block wrong: %#v", content[1])
	}
	// The loop dispatches by ID, so it must be call_id (fc_1 answers nothing).
	if content[2].Type != BlockToolUse || content[2].ID != "call_9" || content[2].Name != "read" {
		t.Errorf("tool_use block wrong: %#v", content[2])
	}
	if out.Usage.InputTokens != 11 || out.Usage.OutputTokens != 22 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

// Truncation is reported by status + incomplete_details here, not by a
// finish_reason on a choice.
func TestResponsesTruncationMapsToMaxTokens(t *testing.T) {
	var out orResponse
	if err := json.Unmarshal([]byte(
		`{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
	), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "incomplete" || out.IncompleteDetails == nil || out.IncompleteDetails.Reason != "max_output_tokens" {
		t.Fatalf("incomplete_details not decoded: %+v", out)
	}
}

// Pictures are asked for explicitly. Without this the search returns prose only,
// which is how an agent sent to look at a diagram could read every word around it
// and still not have seen it: the page reader opens a .png and finds no text.
func TestResponsesWebSearchAsksForImages(t *testing.T) {
	m := orEncoded(t, Request{
		Model:     "gpt-6-astra",
		MaxTokens: 100,
		Messages:  []Message{{Role: "user", Content: []Block{TextBlock("look at the diagram")}}},
		Tools:     []Tool{{Name: "web_search", Type: WebSearchTool, MaxUses: 5}},
	})
	tools, _ := m["tools"].([]any)
	tool, _ := tools[0].(map[string]any)

	types, _ := tool["search_content_types"].([]any)
	if len(types) != 2 {
		t.Fatalf("search_content_types = %#v, want text and image", tool["search_content_types"])
	}
	seen := map[string]bool{}
	for _, v := range types {
		s, _ := v.(string)
		seen[s] = true
	}
	if !seen["image"] || !seen["text"] {
		t.Errorf("search_content_types = %#v, want both", types)
	}

	// The API has no default for how many pictures come back, and every one the
	// model looks at is paid for in tokens, so a positive cap is always sent.
	settings, ok := tool["image_settings"].(map[string]any)
	if !ok {
		t.Fatalf("no image_settings on the grant: %#v", tool)
	}
	if n, _ := settings["max_results"].(float64); n <= 0 {
		t.Errorf("max_results = %v, want a positive cap", settings["max_results"])
	}
}

// The pictures a search found only come back when the request asks for the
// results array. Without it every search reports zero images — which is what
// twenty-one searches in a row did, while the model may well have been looking
// at pictures the whole time.
func TestResponsesAsksForSearchResults(t *testing.T) {
	withSearch := orEncoded(t, Request{
		Model:        "gpt-6-astra",
		MaxTokens:    100,
		Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		Tools:        []Tool{{Name: "web_search", Type: WebSearchTool, MaxUses: 5}},
		OutputConfig: &OutputConfig{Effort: "high"},
	})
	if !includes(withSearch, "web_search_call.results") {
		t.Errorf("include = %#v, want the search results array", withSearch["include"])
	}

	// Asked for only where a search can happen: naming a tool the request does
	// not carry is a field asking about something impossible.
	withoutSearch := orEncoded(t, Request{
		Model:        "gpt-6-astra",
		MaxTokens:    100,
		Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
		OutputConfig: &OutputConfig{Effort: "high"},
	})
	if includes(withoutSearch, "web_search_call.results") {
		t.Errorf("include = %#v, want no search results on a request with no search", withoutSearch["include"])
	}
	// The reasoning state still has to survive either way.
	if !includes(withoutSearch, "reasoning.encrypted_content") {
		t.Errorf("include = %#v, want the encrypted reasoning kept", withoutSearch["include"])
	}
}

func includes(m map[string]any, want string) bool {
	list, _ := m["include"].([]any)
	for _, v := range list {
		if s, _ := v.(string); s == want {
			return true
		}
	}
	return false
}
