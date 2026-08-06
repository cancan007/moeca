package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
	"orchestra/agent/internal/tools"
)

// A web-search turn is the one shape the loop had never seen: the provider runs
// the tool, so the response carries a server_tool_use block the agent must NOT
// dispatch, and it can stop with "pause_turn" — unfinished rather than over.
// The old default branch treated that as an unexpected stop and ended the run
// mid-search. This drives the whole thing end to end.
func TestLoopResumesPausedWebSearch(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		var req llm.Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		calls++

		if calls == 1 {
			assertWebSearchAdvertised(t, req)
			// A paused turn: the search ran on the provider's side and both
			// blocks come back together, with no tool_use for the agent.
			w.Write([]byte(`{
				"id": "msg_1", "model": "claude-opus-4-8", "role": "assistant",
				"stop_reason": "pause_turn",
				"content": [
					{"type": "server_tool_use", "id": "srvtoolu_1", "name": "web_search",
					 "input": {"query": "orchestra release notes"}},
					{"type": "web_search_tool_result", "tool_use_id": "srvtoolu_1",
					 "content": [{"type": "web_search_result", "url": "https://example.com", "title": "Example"}]}
				],
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`))
			return
		}

		// Resuming means echoing the paused turn back verbatim and adding
		// nothing: the agent has no result to contribute.
		if len(req.Messages) != 2 {
			t.Fatalf("resume request messages = %d, want 2 (user task + paused assistant)", len(req.Messages))
		}
		asst := req.Messages[1]
		if asst.Role != "assistant" {
			t.Fatalf("messages[1].role = %q, want assistant", asst.Role)
		}
		for _, want := range []string{"server_tool_use", "web_search_tool_result"} {
			if !hasBlockType(asst.Content, want) {
				t.Errorf("%s block dropped from the echoed turn", want)
			}
		}
		if b := findBlock(asst.Content, "web_search_tool_result"); b != nil && !strings.Contains(string(b.Raw), "example.com") {
			t.Errorf("search result not preserved verbatim: %s", b.Raw)
		}

		w.Write([]byte(`{
			"id": "msg_2", "model": "claude-opus-4-8", "role": "assistant",
			"stop_reason": "end_turn",
			"content": [{"type": "text", "text": "Found it."}],
			"usage": {"input_tokens": 20, "output_tokens": 8}
		}`))
	}))
	defer srv.Close()

	reg := tools.New(t.TempDir())
	reg.SetWebSearch(tools.WebSearchConfig{MaxUses: 3})

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model:    "claude-opus-4-8",
		Task:     "find the release notes",
		Provider: llm.New(srv.URL, nil),
		Tools:    reg,
		LogW:     &logs,
	})
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 API calls (pause + resume), got %d", calls)
	}

	// The search has to show up in the run log. It never reaches executeTools,
	// so without server-tool names a searching turn reads as a turn that used
	// no tools at all — which is exactly how the missing feature looked.
	if !strings.Contains(logs.String(), `"web_search"`) {
		t.Errorf("run log does not mention the search:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), `"task_done"`) {
		t.Errorf("run did not complete:\n%s", logs.String())
	}
}

// An agent that was not granted search must not be able to ask for it.
func TestWebSearchNotAdvertisedWithoutGrant(t *testing.T) {
	for _, def := range tools.New(t.TempDir()).Definitions() {
		if def.Name == tools.WebSearchToolName {
			t.Fatalf("web_search advertised without a grant")
		}
	}
}

func assertWebSearchAdvertised(t *testing.T, req llm.Request) {
	t.Helper()
	for _, def := range req.Tools {
		if def.Name != tools.WebSearchToolName {
			continue
		}
		if def.Type != llm.WebSearchTool {
			t.Errorf("web_search type = %q, want %q", def.Type, llm.WebSearchTool)
		}
		if def.MaxUses != 3 {
			t.Errorf("web_search max_uses = %d, want 3", def.MaxUses)
		}
		return
	}
	t.Fatalf("web_search was not advertised; tools = %+v", req.Tools)
}

func hasBlockType(blocks []llm.Block, typ string) bool { return findBlock(blocks, typ) != nil }

func findBlock(blocks []llm.Block, typ string) *llm.Block {
	for i := range blocks {
		if blocks[i].Type == typ {
			return &blocks[i]
		}
	}
	return nil
}
