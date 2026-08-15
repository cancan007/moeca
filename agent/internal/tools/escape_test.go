package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
)

// A body template's placeholders sit inside JSON string literals, and the
// values come from a model. A prompt with a line break or a quotation mark
// produced a body the provider could not parse — and it answered `invalid_json`
// with no hint, so the model rewrote its prompt three times while the words
// were never the problem.

func echoBodyTool(t *testing.T, def HTTPTool) (*Registry, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("the provider could not parse the body: %v", err)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("bytes"))
	}))
	t.Cleanup(srv.Close)
	r := New(t.TempDir())
	r.SetHTTP(srv.URL, llm.GatewayCtx{}, []HTTPTool{def})
	return r, &got
}

func TestAMultilinePromptStaysValidJSON(t *testing.T) {
	r, got := echoBodyTool(t, HTTPTool{
		Name: "gen", Method: "POST", Path: "/x",
		Body:   `{"model":"m","prompt":"{{prompt}}"}`,
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	prompt := "0-1s: stands and waves\n1-2.5s: sways \"gently\"\n\tand ends with a \\pose"

	if out, isErr := r.Dispatch("gen", map[string]any{"prompt": prompt, "path": "o.png"}); isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	// The value must arrive intact, not merely parseable.
	if (*got)["prompt"] != prompt {
		t.Errorf("prompt round-tripped as %q", (*got)["prompt"])
	}
}

func TestQuotesInAValueCannotBreakOutOfTheField(t *testing.T) {
	r, got := echoBodyTool(t, HTTPTool{
		Name: "gen", Method: "POST", Path: "/x",
		Body:   `{"model":"m","prompt":"{{prompt}}"}`,
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	// A value that would close the string and inject a field of its own.
	r.Dispatch("gen", map[string]any{"prompt": `x","model":"injected`, "path": "o.png"})

	if (*got)["model"] != "m" {
		t.Errorf("model = %v, want the template's own value", (*got)["model"])
	}
}

// The same escaping keeps a multipart tool's fields parseable, since its form
// fields are read out of that JSON body.
func TestAMultipartToolTakesAMultilinePrompt(t *testing.T) {
	var gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("multipart parse: %v", err)
		}
		gotPrompt = req.FormValue("prompt")
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	work := t.TempDir()
	r := New(work)
	r.SetHTTP(srv.URL, llm.GatewayCtx{}, []HTTPTool{{
		Name: "edit", Method: "POST", Path: "/x",
		Body:   `{"model":"m","prompt":"{{prompt}}"}`,
		Inputs: map[string]ToolInput{"image": {As: "multipart"}},
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	}})
	os.WriteFile(filepath.Join(work, "a.png"), []byte("\x89PNG"), 0o644)

	prompt := "line one\nline \"two\""
	if out, isErr := r.Dispatch("edit", map[string]any{"prompt": prompt, "image": "a.png", "path": "o.png"}); isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	if gotPrompt != prompt {
		t.Errorf("form prompt = %q, want it intact", gotPrompt)
	}
}

// Escaping applies to the body, not the path: a URL is not JSON and escaping it
// would corrupt it.
func TestThePathIsNotJSONEscaped(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	r := New(t.TempDir())
	r.SetHTTP(srv.URL, llm.GatewayCtx{}, []HTTPTool{{Name: "t", Path: "/x/{{id}}", Method: "GET"}})

	r.Dispatch("t", map[string]any{"id": "abc"})
	if !strings.HasSuffix(gotPath, "/x/abc") {
		t.Errorf("path = %q", gotPath)
	}
}
