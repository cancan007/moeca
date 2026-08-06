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

// TestLoopWritesFileAndTerminates drives the whole loop against a mock Messages
// API. The first response asks to write hello.txt via the write_file tool; the
// second returns end_turn. We assert the file landed in the temp workdir and
// the loop exited cleanly.
func TestLoopWritesFileAndTerminates(t *testing.T) {
	workdir := t.TempDir()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		// The agent must NOT set x-api-key or anthropic-version — the gateway does.
		if r.Header.Get("x-api-key") != "" {
			t.Errorf("agent set x-api-key; gateway should inject it")
		}
		if r.Header.Get("anthropic-version") != "" {
			t.Errorf("agent set anthropic-version; gateway should inject it")
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		w.Header().Set("Content-Type", "application/json")
		calls++
		if calls == 1 {
			w.Write([]byte(`{
				"id": "msg_1",
				"model": "claude-opus-4-8",
				"role": "assistant",
				"stop_reason": "tool_use",
				"content": [
					{"type": "thinking", "thinking": "I should write the file", "signature": "abc"},
					{"type": "tool_use", "id": "toolu_1", "name": "write_file",
					 "input": {"path": "hello.txt", "content": "hi there"}}
				],
				"usage": {"input_tokens": 10, "output_tokens": 5}
			}`))
			return
		}

		// Second call: verify the assistant turn (thinking + tool_use) was
		// echoed back verbatim and the tool_result was appended.
		var req llm.Request
		body, _ := readAll(r)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode second request: %v", err)
		}
		assertSecondTurn(t, req)

		w.Write([]byte(`{
			"id": "msg_2",
			"model": "claude-opus-4-8",
			"role": "assistant",
			"stop_reason": "end_turn",
			"content": [{"type": "text", "text": "Done."}],
			"usage": {"input_tokens": 20, "output_tokens": 8}
		}`))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model:  "claude-opus-4-8",
		System: "test system",
		Task:   "create hello.txt",
		Provider: llm.New(srv.URL, nil),
		Tools:  tools.New(workdir),
		LogW:   &logs,
	})

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if calls != 2 {
		t.Fatalf("expected 2 API calls, got %d", calls)
	}

	got, err := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if err != nil {
		t.Fatalf("hello.txt not written: %v", err)
	}
	if string(got) != "hi there" {
		t.Fatalf("hello.txt = %q, want %q", got, "hi there")
	}

	// Log lines should include a task_done event.
	if !strings.Contains(logs.String(), `"task_done"`) {
		t.Fatalf("logs missing task_done event:\n%s", logs.String())
	}
	// Each log line should be valid JSON.
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line not valid JSON: %q (%v)", line, err)
		}
	}
}

func assertSecondTurn(t *testing.T, req llm.Request) {
	t.Helper()
	// messages: [user task, assistant(thinking+tool_use), user(tool_result)]
	if len(req.Messages) != 3 {
		t.Fatalf("second request messages = %d, want 3", len(req.Messages))
	}
	asst := req.Messages[1]
	if asst.Role != "assistant" {
		t.Fatalf("messages[1].role = %q, want assistant", asst.Role)
	}
	// The thinking block must have been preserved verbatim.
	foundThinking := false
	for _, b := range asst.Content {
		if b.Type == "thinking" {
			foundThinking = true
			if !strings.Contains(string(b.Raw), "signature") {
				t.Errorf("thinking block not preserved verbatim: %s", b.Raw)
			}
		}
	}
	if !foundThinking {
		t.Errorf("thinking block was dropped from echoed assistant turn")
	}
	tr := req.Messages[2]
	if tr.Role != "user" || len(tr.Content) != 1 || tr.Content[0].Type != llm.BlockToolResult {
		t.Fatalf("messages[2] is not a tool_result user turn: %+v", tr)
	}
	if tr.Content[0].ToolUseID != "toolu_1" {
		t.Errorf("tool_result tool_use_id = %q, want toolu_1", tr.Content[0].ToolUseID)
	}
}

// TestLoopMaxTokensStops verifies a max_tokens stop reason ends the loop cleanly.
func TestLoopMaxTokensStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"id": "msg_1", "model": "m", "role": "assistant",
			"stop_reason": "max_tokens",
			"content": [{"type": "text", "text": "trunc"}],
			"usage": {"input_tokens": 1, "output_tokens": 16000}
		}`))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model: "m", Task: "x",
		Provider: llm.New(srv.URL, nil),
		Tools:  tools.New(t.TempDir()),
		LogW:   &logs,
	})
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(logs.String(), `"task_stopped"`) {
		t.Fatalf("expected task_stopped log, got:\n%s", logs.String())
	}
}

func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
