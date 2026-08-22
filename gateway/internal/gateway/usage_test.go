package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestra/gateway/internal/config"
)

func TestExtractUsage(t *testing.T) {
	cases := []struct {
		name     string
		dialect  string
		body     string
		streamed bool
		in, out  int
		ok       bool
	}{
		{"anthropic", "anthropic", `{"content":[],"usage":{"input_tokens":100,"output_tokens":25}}`, false, 100, 25, true},
		{"openai", "openai", `{"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":60}}`, false, 40, 60, true},
		{"gemini", "gemini", `{"candidates":[],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`, false, 7, 3, true},
		// custom-named provider: fall back to probing all dialects
		{"custom-openai", "my-azure", `{"usage":{"prompt_tokens":5,"completion_tokens":9}}`, false, 5, 9, true},
		// streaming: last usage value wins (Anthropic message_delta after message_start)
		{"stream-last-wins", "anthropic", `data: {"usage":{"input_tokens":10,"output_tokens":1}}` + "\n" + `data: {"usage":{"input_tokens":10,"output_tokens":42}}`, true, 10, 42, true},

		// An embeddings response reports what it consumed and no completion
		// count, because nothing was completed. Requiring both counts sent this
		// to the byte estimate, which then read a megabyte of returned vectors
		// as if the model had written it.
		{"embeddings", "openai", `{"data":[],"model":"text-embedding-3-small","usage":{"prompt_tokens":10232,"total_tokens":10232}}`, false, 10232, 0, true},
		// A reported total settles the split without guessing.
		{"total-only", "openai", `{"usage":{"total_tokens":90}}`, false, 90, 0, true},
		{"total-minus-input", "openai", `{"usage":{"prompt_tokens":30,"total_tokens":100}}`, false, 30, 70, true},

		// In a stream the counts arrive in different frames, so a missing one
		// may simply have scrolled past the tail: charging the visible half
		// would undercount, and the estimate is the safer answer.
		{"streamed-partial", "anthropic", `data: {"usage":{"output_tokens":25}}`, true, 0, 0, false},
		// In one whole JSON body there is no such doubt: absent means absent.
		{"whole-body-partial", "anthropic", `{"usage":{"output_tokens":25}}`, false, 0, 25, true},
		{"none", "anthropic", `{"content":[]}`, false, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, out, ok := extractUsage([]byte(c.body), c.dialect, c.streamed)
			if ok != c.ok || (ok && (in != c.in || out != c.out)) {
				t.Errorf("extractUsage(%s) = (%d,%d,%v), want (%d,%d,%v)", c.dialect, in, out, ok, c.in, c.out, c.ok)
			}
		})
	}
}

// TestRealTokenUsageCharged asserts a model service is billed the response's
// REAL token usage (input+output), not the byte estimate.
func TestRealTokenUsageCharged(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":25}}`)
	}))
	defer up.Close()

	cfg := baseConfig(up.URL)
	cfg.Services["anthropic"] = config.Service{
		Kind: "model", Prefix: "/anthropic/", Upstream: up.URL,
		Allowlist: []string{"127.0.0.1"},
		Budget:    config.Budget{MaxTokensPerSession: 100000},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "POST", "/anthropic/v1/messages", map[string]string{SessionHeader: "tok"}, `{"model":"claude","messages":[]}`)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	st := do(t, srv, "GET", "/_gateway/status", map[string]string{AdminHeader: "admintok"}, "")
	defer st.Body.Close()
	var out struct {
		SpentTokens map[string]int64 `json:"spentTokens"`
	}
	json.NewDecoder(st.Body).Decode(&out)
	if got := out.SpentTokens["s1|anthropic"]; got != 125 {
		t.Errorf("charged %d tokens, want 125 (real usage 100+25, not byte estimate)", got)
	}
}
