package tools

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
)

// TestHTTPToolThroughGateway verifies a custom HTTP tool: {{param}} substitution
// into path/body, the gateway session header, and the response returned to the
// model. The mock stands in for the gateway.
func TestHTTPToolThroughGateway(t *testing.T) {
	var gotPath, gotSession, gotBody string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSession = r.Header.Get("X-Orchestra-Session")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer gw.Close()

	r := New(t.TempDir())
	r.SetHTTP(gw.URL, llm.GatewayCtx{Session: "sess-123"}, []HTTPTool{{
		Name: "post_message", Description: "post", Method: "POST",
		Path: "/slack/channels/{{channel}}/messages",
		Body: `{"text":"{{text}}"}`,
	}})

	// Tool appears in Definitions alongside the file tools.
	found := false
	for _, d := range r.Definitions() {
		if d.Name == "post_message" {
			found = true
		}
	}
	if !found {
		t.Fatalf("post_message not advertised in Definitions")
	}

	out, isErr := r.Dispatch("post_message", map[string]any{"channel": "general", "text": "hi"})
	if isErr {
		t.Fatalf("tool errored: %s", out)
	}
	if gotPath != "/slack/channels/general/messages" {
		t.Errorf("path = %q, want substituted", gotPath)
	}
	if gotSession != "sess-123" {
		t.Errorf("session header = %q, want sess-123", gotSession)
	}
	if gotBody != `{"text":"hi"}` {
		t.Errorf("body = %q, want substituted", gotBody)
	}
	if !strings.Contains(out, "HTTP 200") || !strings.Contains(out, `"ok":true`) {
		t.Errorf("tool result = %q, want status + body", out)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)

	out, isErr := r.Dispatch("write_file", map[string]any{"path": "a/b.txt", "content": "hello"})
	if isErr {
		t.Fatalf("write_file errored: %s", out)
	}

	got, err := os.ReadFile(filepath.Join(dir, "a", "b.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}

	rout, isErr := r.Dispatch("read_file", map[string]any{"path": "a/b.txt"})
	if isErr {
		t.Fatalf("read_file errored: %s", rout)
	}
	if rout != "hello" {
		t.Fatalf("read = %q, want %q", rout, "hello")
	}
}

func TestReadMissingFileIsError(t *testing.T) {
	r := New(t.TempDir())
	out, isErr := r.Dispatch("read_file", map[string]any{"path": "nope.txt"})
	if !isErr {
		t.Fatalf("expected error reading missing file, got %q", out)
	}
}

func TestEditFileSingleOccurrence(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one two three"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, isErr := r.Dispatch("edit_file", map[string]any{
		"path": "f.txt", "old_str": "two", "new_str": "TWO",
	})
	if isErr {
		t.Fatalf("edit_file errored: %s", out)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(got) != "one TWO three" {
		t.Fatalf("content = %q, want %q", got, "one TWO three")
	}
}

func TestEditFileZeroMatchesIsError(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("abc"), 0o644)

	out, isErr := r.Dispatch("edit_file", map[string]any{
		"path": "f.txt", "old_str": "xyz", "new_str": "q",
	})
	if !isErr {
		t.Fatalf("expected error for 0 matches, got %q", out)
	}
}

func TestEditFileMultipleMatchesIsError(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x x x"), 0o644)

	out, isErr := r.Dispatch("edit_file", map[string]any{
		"path": "f.txt", "old_str": "x", "new_str": "y",
	})
	if !isErr {
		t.Fatalf("expected error for >1 matches, got %q", out)
	}
	if !strings.Contains(out, "3 times") {
		t.Fatalf("error should report the count, got %q", out)
	}
}

func TestListFiles(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("b"), 0o644)
	// .git contents must be skipped.
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref"), 0o644)

	out, isErr := r.Dispatch("list_files", nil)
	if isErr {
		t.Fatalf("list_files errored: %s", out)
	}
	if !strings.Contains(out, "root.txt") || !strings.Contains(out, filepath.Join("sub", "nested.txt")) {
		t.Fatalf("list missing expected files: %q", out)
	}
	if strings.Contains(out, "HEAD") {
		t.Fatalf("list should skip .git, got %q", out)
	}
}

func TestListFilesSubdir(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "root.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("b"), 0o644)

	out, isErr := r.Dispatch("list_files", map[string]any{"subdir": "sub"})
	if isErr {
		t.Fatalf("list_files errored: %s", out)
	}
	if strings.Contains(out, "root.txt") {
		t.Fatalf("subdir list should not include root.txt: %q", out)
	}
	if !strings.Contains(out, "nested.txt") {
		t.Fatalf("subdir list should include nested.txt: %q", out)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	r := New(dir)

	cases := []string{"../etc/x", "../../evil", "/etc/passwd", ".."}
	for _, p := range cases {
		out, isErr := r.Dispatch("write_file", map[string]any{"path": p, "content": "x"})
		if !isErr {
			t.Fatalf("path %q should be rejected, got %q", p, out)
		}
		// And nothing should have been written outside the root.
	}

	// A normal relative path still works.
	out, isErr := r.Dispatch("write_file", map[string]any{"path": "ok.txt", "content": "x"})
	if isErr {
		t.Fatalf("normal path rejected: %s", out)
	}
}

func TestUnknownToolIsError(t *testing.T) {
	r := New(t.TempDir())
	out, isErr := r.Dispatch("frobnicate", nil)
	if !isErr {
		t.Fatalf("unknown tool should error, got %q", out)
	}
}

func TestDefinitionsCoverAllTools(t *testing.T) {
	r := New(t.TempDir())
	defs := r.Definitions()
	want := map[string]bool{"list_files": false, "read_file": false, "write_file": false, "edit_file": false}
	for _, d := range defs {
		if _, ok := want[d.Name]; ok {
			want[d.Name] = true
		}
		if d.InputSchema == nil {
			t.Fatalf("tool %q has no input schema", d.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("tool %q missing from definitions", name)
		}
	}
}
