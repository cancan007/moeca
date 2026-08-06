package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
	"orchestra/agent/internal/tools"
)

// fakeProvider is an in-memory llm.Provider that records every request and
// returns scripted responses via fn.
type fakeProvider struct {
	reqs []llm.Request
	fn   func(i int, req llm.Request) *llm.Response
}

func (p *fakeProvider) CreateMessage(_ context.Context, req llm.Request) (*llm.Response, error) {
	p.reqs = append(p.reqs, req)
	return p.fn(len(p.reqs)-1, req), nil
}

func textResp(text string, in int, stop string) *llm.Response {
	return &llm.Response{Role: "assistant", StopReason: stop, Content: []llm.Block{llm.TextBlock(text)}, Usage: llm.Usage{InputTokens: in}}
}

func toolUseResp(id string, in int) *llm.Response {
	return &llm.Response{Role: "assistant", StopReason: "tool_use", Usage: llm.Usage{InputTokens: in},
		Content: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "noop", Input: json.RawMessage(`{}`)}}}
}

// pairedHistory builds a task turn followed by n tool_use/tool_result pairs.
func pairedHistory(n int) []llm.Message {
	msgs := []llm.Message{{Role: "user", Content: []llm.Block{llm.TextBlock("TASK")}}}
	for i := 0; i < n; i++ {
		id := "t" + string(rune('a'+i))
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: []llm.Block{{Type: llm.BlockToolUse, ID: id, Name: "read", Input: json.RawMessage(`{"p":"x"}`)}}},
			llm.Message{Role: "user", Content: []llm.Block{llm.ToolResultBlock(id, "result-"+id, false)}},
		)
	}
	return msgs
}

func assertAlternating(t *testing.T, msgs []llm.Message) {
	t.Helper()
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Role == msgs[i-1].Role {
			t.Fatalf("roles do not alternate at %d: %s then %s", i, msgs[i-1].Role, msgs[i].Role)
		}
	}
	if len(msgs) > 0 && msgs[0].Role != "user" {
		t.Fatalf("history must start with user, got %s", msgs[0].Role)
	}
}

// toolUseIDs / toolResultIDs collect the tool_use and tool_result ids so a test
// can assert no tool_result is left without its originating tool_use.
func danglingToolResults(msgs []llm.Message) []string {
	seen := map[string]bool{}
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolUse {
				seen[b.ID] = true
			}
		}
	}
	var dangling []string
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolResult && !seen[b.ToolUseID] {
				dangling = append(dangling, b.ToolUseID)
			}
		}
	}
	return dangling
}

func TestMaybeCompactSummarizesMiddle(t *testing.T) {
	prov := &fakeProvider{fn: func(_ int, req llm.Request) *llm.Response {
		if req.System != summarySystem {
			t.Fatalf("expected summarizer call, got system %q", req.System)
		}
		if len(req.Tools) != 0 {
			t.Errorf("summarizer request should carry no tools")
		}
		return textResp("BRIEF-SUMMARY", 5, "end_turn")
	}}
	r := NewRunner(Config{Task: "TASK", Provider: prov, Tools: tools.New(t.TempDir()), MaxContextTokens: 100, KeepRecent: 2})

	msgs := pairedHistory(3) // len 7: user + 3*(assistant,user)
	out, did := r.maybeCompact(context.Background(), msgs, 500)
	if !did {
		t.Fatal("expected compaction to fire")
	}
	if len(out) >= len(msgs) {
		t.Errorf("history did not shrink: %d -> %d", len(msgs), len(out))
	}
	if out[0].Role != "user" || !strings.Contains(out[0].Content[0].Text, "TASK") || !strings.Contains(out[0].Content[0].Text, "BRIEF-SUMMARY") {
		t.Errorf("head turn missing task or summary: %+v", out[0].Content)
	}
	if out[1].Role != "assistant" {
		t.Errorf("tail must start on assistant, got %s", out[1].Role)
	}
	assertAlternating(t, out)
	if d := danglingToolResults(out); len(d) != 0 {
		t.Errorf("dangling tool_result ids after compaction: %v", d)
	}
	// exactly one summarizer call was made
	if len(prov.reqs) != 1 {
		t.Errorf("summarizer calls = %d, want 1", len(prov.reqs))
	}
}

func TestMaybeCompactDisabledAndBelowThreshold(t *testing.T) {
	prov := &fakeProvider{fn: func(_ int, _ llm.Request) *llm.Response { return textResp("x", 1, "end_turn") }}

	// disabled (MaxContextTokens == 0)
	r0 := NewRunner(Config{Task: "T", Provider: prov, Tools: tools.New(t.TempDir()), MaxContextTokens: 0, KeepRecent: 2})
	if _, did := r0.maybeCompact(context.Background(), pairedHistory(3), 999999); did {
		t.Error("compaction should be disabled at MaxContextTokens=0")
	}

	// below threshold -> no compaction, no summarizer call
	r1 := NewRunner(Config{Task: "T", Provider: prov, Tools: tools.New(t.TempDir()), MaxContextTokens: 100000, KeepRecent: 2})
	if _, did := r1.maybeCompact(context.Background(), pairedHistory(3), 50); did {
		t.Error("compaction should not fire below threshold")
	}
	if len(prov.reqs) != 0 {
		t.Errorf("summarizer must not be called when not compacting; got %d calls", len(prov.reqs))
	}
}

func TestSafeCut(t *testing.T) {
	msgs := pairedHistory(3) // indices: 0u 1a 2u 3a 4u 5a 6u
	if got := safeCut(msgs, 2); got != 5 {
		t.Errorf("safeCut keep=2 = %d, want 5 (assistant boundary)", got)
	}
	if got := safeCut(msgs, 100); got != 0 {
		t.Errorf("safeCut with keep>len should be 0, got %d", got)
	}
}

func TestRunLoopCompactsMidRun(t *testing.T) {
	var mainCalls int
	prov := &fakeProvider{}
	prov.fn = func(_ int, req llm.Request) *llm.Response {
		if req.System == summarySystem {
			return textResp("MIDRUN-SUMMARY", 5, "end_turn")
		}
		// verify the history the model receives is always well-formed
		assertAlternating(t, req.Messages)
		if d := danglingToolResults(req.Messages); len(d) != 0 {
			t.Fatalf("model received dangling tool_result: %v", d)
		}
		mainCalls++
		if mainCalls >= 5 {
			return textResp("done", 500, "end_turn")
		}
		return toolUseResp("call"+string(rune('a'+mainCalls)), 500) // 500 > threshold
	}

	r := NewRunner(Config{Task: "TASK", Provider: prov, Tools: tools.New(t.TempDir()), MaxContextTokens: 100, KeepRecent: 2, MaxIter: 20})
	if err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// at least one summarizer request happened
	var summarizerCalls int
	for _, rq := range prov.reqs {
		if rq.System == summarySystem {
			summarizerCalls++
		}
	}
	if summarizerCalls == 0 {
		t.Error("expected compaction to summarize at least once during the run")
	}
}
