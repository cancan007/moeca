package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// The cap has to hold against a provider that does not enforce it.
//
// Anthropic is told max_uses and honours it; the OpenAI Responses API has no
// such field, so the only thing standing between a grant of three searches and
// an unbounded bill is this agent counting what came back and withdrawing the
// tool. The stub here plays the second kind of provider: it searches on every
// turn regardless of what it was granted.
func TestWebSearchCapWithheldFromAProviderThatIgnoresIt(t *testing.T) {
	var calls int
	var advertised []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readAll(r)
		var req llm.Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		offered := false
		for _, tool := range req.Tools {
			if tool.Type == llm.WebSearchTool {
				offered = true
			}
		}
		advertised = append(advertised, offered)

		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls <= 4 {
			// Two searches per turn, so the grant of three is passed mid-turn —
			// the overshoot a client-side cap cannot prevent, only report.
			w.Write([]byte(`{
				"id": "msg", "model": "claude-opus-4-8", "role": "assistant",
				"stop_reason": "pause_turn",
				"content": [
					{"type": "server_tool_use", "id": "s1", "name": "web_search",
					 "input": {"query": "one"}},
					{"type": "web_search_tool_result", "tool_use_id": "s1", "content": []},
					{"type": "server_tool_use", "id": "s2", "name": "web_search",
					 "input": {"query": "two"}},
					{"type": "web_search_tool_result", "tool_use_id": "s2", "content": []}
				],
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`))
			return
		}
		w.Write([]byte(`{
			"id": "msg_done", "model": "claude-opus-4-8", "role": "assistant",
			"stop_reason": "end_turn",
			"content": [{"type": "text", "text": "done"}],
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`))
	}))
	defer srv.Close()

	reg := tools.New(t.TempDir())
	reg.SetWebSearch(tools.WebSearchConfig{MaxUses: 3})

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model:    "claude-opus-4-8",
		Task:     "search until stopped",
		Provider: llm.New(srv.URL, nil),
		Tools:    reg,
		LogW:     &logs,
	})
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(advertised) < 2 {
		t.Fatalf("only %d requests were made; the loop stopped too early", len(advertised))
	}
	if !advertised[0] {
		t.Error("the grant was not offered on the first request")
	}
	// Three were granted and the first turn spent two; the second turn takes it
	// past the cap, so from the third request on the tool is gone.
	if advertised[len(advertised)-1] {
		t.Errorf("the grant was still offered after it was spent: %v", advertised)
	}
	if !strings.Contains(logs.String(), `"web_search_exhausted"`) {
		t.Errorf("the run log does not say the grant was withdrawn:\n%s", logs.String())
	}
	// The count that is logged is what actually happened, not what was granted.
	if !strings.Contains(logs.String(), `"count":4`) {
		t.Errorf("the log does not carry the real number of searches:\n%s", logs.String())
	}
}

// A run that dies mid-flight still reports what it had said.
//
// The error path used to return an empty summary, so a turn that had reasoned,
// written a file and then lost the connection came back blank — as though it had
// never spoken. It happened for real: a five-minute timeout during provider-side
// search threw away an answer that was most of the way written.
func TestLoopKeepsWhatItSaidWhenTheCallFails(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readAll(r)
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"id": "msg_1", "model": "claude-opus-4-8", "role": "assistant",
				"stop_reason": "tool_use",
				"content": [
					{"type": "text", "text": "Here is the shape of the answer so far."},
					{"type": "tool_use", "id": "t1", "name": "list_files", "input": {}}
				],
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`))
			return
		}
		// The second call never answers — the shape a timeout or a dropped
		// gateway takes from in here.
		http.Error(w, "upstream gone", http.StatusBadGateway)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model:    "claude-opus-4-8",
		Task:     "do the thing",
		Provider: llm.New(srv.URL, nil),
		Tools:    tools.New(dir),
		LogW:     &logs,
		Workdir:  dir,
		StageID:  "planner",
	})
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("Run returned no error; the second call failed")
	}

	// The manifest is where the caller reads a stage's account of itself, and a
	// failed stage still has one.
	raw, err := os.ReadFile(filepath.Join(dir, ".orchestra", "stages", "planner.json"))
	if err != nil {
		t.Fatalf("no manifest was written: %v", err)
	}
	var m struct {
		Summary string `json:"summary"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Summary, "the shape of the answer") {
		t.Errorf("summary = %q, want what the assistant had already said", m.Summary)
	}
	if m.Error == "" {
		t.Error("the manifest does not record that the run failed")
	}
}
