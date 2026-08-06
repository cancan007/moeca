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

// TestLoopCustomHTTPTool drives the full loop where the model calls a custom
// HTTP tool: turn 1 returns a tool_use for "http_ping"; the agent executes it
// through the (mock) gateway; turn 2 sees the tool_result and ends. One mock
// server plays both the LLM endpoint and the tool's gateway route.
func TestLoopCustomHTTPTool(t *testing.T) {
	var llmCalls int
	var toolHit, toolSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/anthropic/v1/messages":
			w.Header().Set("Content-Type", "application/json")
			llmCalls++
			if llmCalls == 1 {
				w.Write([]byte(`{"id":"m1","model":"m","role":"assistant","stop_reason":"tool_use",
					"content":[{"type":"tool_use","id":"t1","name":"http_ping","input":{"msg":"hello"}}],
					"usage":{"input_tokens":1,"output_tokens":1}}`))
				return
			}
			// turn 2: the tool result must have been fed back.
			var req llm.Request
			body, _ := readAll(r)
			json.Unmarshal(body, &req)
			foundResult := false
			for _, m := range req.Messages {
				for _, b := range m.Content {
					if b.Type == llm.BlockToolResult && strings.Contains(b.Content, "pong") {
						foundResult = true
					}
				}
			}
			if !foundResult {
				t.Errorf("tool result (pong) not fed back to the model: %s", body)
			}
			w.Write([]byte(`{"id":"m2","model":"m","role":"assistant","stop_reason":"end_turn",
				"content":[{"type":"text","text":"done"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
		case strings.HasPrefix(r.URL.Path, "/ping/"):
			toolHit = r.URL.Path
			toolSession = r.Header.Get("X-Orchestra-Session")
			w.Write([]byte("pong"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	reg := tools.New(t.TempDir())
	reg.SetHTTP(srv.URL, llm.GatewayCtx{Session: "sess-abc"}, []tools.HTTPTool{{
		Name: "http_ping", Description: "ping the gateway", Method: "GET", Path: "/ping/{{msg}}",
	}})

	var logs bytes.Buffer
	runner := NewRunner(Config{
		Model: "m", Task: "ping",
		Provider: llm.NewProvider(llm.KindAnthropic, srv.URL+"/anthropic", llm.GatewayCtx{Session: "sess-abc"}, nil),
		Tools:    reg,
		LogW:     &logs,
	})
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if llmCalls != 2 {
		t.Fatalf("llm calls = %d, want 2", llmCalls)
	}
	if toolHit != "/ping/hello" {
		t.Errorf("tool hit %q, want /ping/hello (param substituted)", toolHit)
	}
	if toolSession != "sess-abc" {
		t.Errorf("tool session header = %q, want sess-abc", toolSession)
	}
	if !strings.Contains(logs.String(), `"task_done"`) {
		t.Errorf("missing task_done:\n%s", logs.String())
	}
}
