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

// TestLoopOpenAI drives the full loop against a mock OpenAI Chat Completions
// upstream: turn 1 asks (via a tool_call) to write hello.txt, turn 2 ends. It
// asserts the file landed and that the tool result was echoed back as a `tool`
// message keyed by the original tool_call id.
func TestLoopOpenAI(t *testing.T) {
	workdir := t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		calls++
		if calls == 1 {
			w.Write([]byte(`{
				"id":"cmpl_1","model":"gpt-4o",
				"choices":[{"message":{"role":"assistant","content":null,
					"tool_calls":[{"id":"call_a","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"hello.txt\",\"content\":\"hi there\"}"}}]},
					"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
			return
		}
		// turn 2: assert the tool result came back as a tool-role message.
		var req struct {
			Messages []struct {
				Role       string `json:"role"`
				ToolCallID string `json:"tool_call_id"`
				Content    string `json:"content"`
			} `json:"messages"`
		}
		body, _ := readAll(r)
		json.Unmarshal(body, &req)
		var toolMsg *struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			Content    string `json:"content"`
		}
		for i := range req.Messages {
			if req.Messages[i].Role == "tool" {
				toolMsg = &req.Messages[i]
			}
		}
		if toolMsg == nil || toolMsg.ToolCallID != "call_a" {
			t.Errorf("tool result not echoed as tool message with id call_a: %+v", req.Messages)
		}
		w.Write([]byte(`{"id":"cmpl_2","model":"gpt-4o",
			"choices":[{"message":{"role":"assistant","content":"Done."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":20,"completion_tokens":8}}`))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model: "gpt-4o", System: "sys", Task: "create hello.txt",
		Provider: llm.NewProvider(llm.KindOpenAI, srv.URL, llm.GatewayCtx{}, nil),
		Tools:    tools.New(workdir),
		LogW:     &logs,
	})
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 {
		t.Fatalf("openai calls = %d, want 2", calls)
	}
	assertWrote(t, workdir, "hi there")
	if !strings.Contains(logs.String(), `"task_done"`) {
		t.Errorf("missing task_done:\n%s", logs.String())
	}
}

// TestLoopGemini drives the full loop against a mock Gemini generateContent
// upstream and asserts the tool result comes back as a functionResponse keyed by
// the function *name* (Gemini has no call ids).
func TestLoopGemini(t *testing.T) {
	workdir := t.TempDir()
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		calls++
		if calls == 1 {
			w.Write([]byte(`{
				"candidates":[{"content":{"role":"model","parts":[
					{"functionCall":{"name":"write_file","args":{"path":"hello.txt","content":"hi there"}}}]},
					"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`))
			return
		}
		// turn 2: assert a functionResponse named write_file was sent back.
		var req struct {
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					FunctionResponse *struct {
						Name string `json:"name"`
					} `json:"functionResponse"`
				} `json:"parts"`
			} `json:"contents"`
		}
		body, _ := readAll(r)
		json.Unmarshal(body, &req)
		found := false
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p.FunctionResponse != nil && p.FunctionResponse.Name == "write_file" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("functionResponse(name=write_file) not sent back: %s", body)
		}
		w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Done."}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":8}}`))
	}))
	defer srv.Close()

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model: "gemini-2.5-pro", System: "sys", Task: "create hello.txt",
		Provider: llm.NewProvider(llm.KindGemini, srv.URL, llm.GatewayCtx{}, nil),
		Tools:    tools.New(workdir),
		LogW:     &logs,
	})
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 {
		t.Fatalf("gemini calls = %d, want 2", calls)
	}
	assertWrote(t, workdir, "hi there")
	if !strings.Contains(logs.String(), `"task_done"`) {
		t.Errorf("missing task_done:\n%s", logs.String())
	}
}

func assertWrote(t *testing.T, workdir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(workdir, "hello.txt"))
	if err != nil {
		t.Fatalf("hello.txt not written: %v", err)
	}
	if string(got) != want {
		t.Fatalf("hello.txt = %q, want %q", got, want)
	}
}
