package llm

import (
	"encoding/json"
	"testing"
)

// Counting provider-side searches is the only account anyone gets of them: the
// agent does not run the tool, so there is no tool_result of its own, and the
// gateway sees one model call whether it searched five times or none.

func block(t *testing.T, v any) Block {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var b Block
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	b.Raw = raw
	return b
}

func TestWebSearchUsesCountsBothDialects(t *testing.T) {
	// Anthropic reports a search as a server_tool_use block…
	anthropic := block(t, map[string]any{
		"type": "server_tool_use", "id": "srvtoolu_1", "name": "web_search",
		"input": map[string]any{"query": "exponential backoff"},
	})
	// …the OpenAI Responses API as a web_search_call output item.
	openai := block(t, map[string]any{
		"type": "web_search_call", "status": "completed",
		"action": map[string]any{"type": "search", "query": "retry jitter"},
	})

	uses := WebSearchUses([]Block{TextBlock("thinking"), anthropic, openai})
	if len(uses) != 2 {
		t.Fatalf("uses = %#v, want one per dialect", uses)
	}
	if uses[0].Query != "exponential backoff" || uses[0].Action != "search" {
		t.Errorf("anthropic use = %#v", uses[0])
	}
	if uses[1].Query != "retry jitter" || uses[1].Action != "search" {
		t.Errorf("openai use = %#v", uses[1])
	}
}

// One OpenAI grant covers more than searching: opening and reading a page are
// separate actions, each billed, so each counts.
func TestWebSearchUsesCountsPageActions(t *testing.T) {
	open := block(t, map[string]any{
		"type":   "web_search_call",
		"action": map[string]any{"type": "open_page", "url": "https://go.dev/blog/retry"},
	})
	find := block(t, map[string]any{
		"type":   "web_search_call",
		"action": map[string]any{"type": "find_in_page", "url": "https://go.dev/blog/retry"},
	})

	uses := WebSearchUses([]Block{open, find})
	if len(uses) != 2 {
		t.Fatalf("uses = %#v, want both actions counted", uses)
	}
	if uses[0].Action != "open_page" || uses[0].Query != "https://go.dev/blog/retry" {
		// Which page was opened is the more interesting fact than an absent query.
		t.Errorf("open_page use = %#v", uses[0])
	}
	if uses[1].Action != "find_in_page" {
		t.Errorf("find_in_page use = %#v", uses[1])
	}
}

// A server tool that is not search must not be counted against the search grant.
func TestWebSearchUsesIgnoresOtherServerTools(t *testing.T) {
	code := block(t, map[string]any{
		"type": "server_tool_use", "id": "srvtoolu_2", "name": "bash_code_execution",
	})
	if uses := WebSearchUses([]Block{code}); len(uses) != 0 {
		t.Errorf("uses = %#v, want none", uses)
	}
}

func TestWebSearchUsesOnAnOrdinaryTurn(t *testing.T) {
	ordinary := []Block{TextBlock("here you go"), {Type: BlockToolUse, Name: "read_file"}}
	if uses := WebSearchUses(ordinary); len(uses) != 0 {
		t.Errorf("uses = %#v, want none", uses)
	}
}

// Image results arrive in the web_search_call item's own `results` array rather
// than in the assistant's message, so counting them here is the only record that
// a picture entered the conversation: the retrieval happened on the provider's
// side and passed no gateway.
func TestWebSearchUsesCountsImageResults(t *testing.T) {
	withImages := block(t, map[string]any{
		"type":   "web_search_call",
		"action": map[string]any{"type": "search", "query": "conceptual diagram"},
		"results": []any{
			map[string]any{"image_url": "https://example.com/a.png", "caption": "a"},
			map[string]any{"image_url": "https://example.com/b.png"},
			// A text result in the same array is not a picture.
			map[string]any{"source_website_url": "https://example.com/article"},
		},
	})
	uses := WebSearchUses([]Block{withImages})
	if len(uses) != 1 {
		t.Fatalf("uses = %#v", uses)
	}
	if uses[0].Images != 2 {
		t.Errorf("Images = %d, want 2", uses[0].Images)
	}
}

func TestWebSearchUsesWithoutImages(t *testing.T) {
	plain := block(t, map[string]any{
		"type": "web_search_call", "action": map[string]any{"type": "search", "query": "x"},
	})
	if uses := WebSearchUses([]Block{plain}); uses[0].Images != 0 {
		t.Errorf("Images = %d, want 0", uses[0].Images)
	}
}
