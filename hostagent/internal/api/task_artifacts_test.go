package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// taskWithArtifacts creates a task and writes files into its worktree, standing
// in for what an agent's generation tools would have produced.
// The worktree root is shared across tests in this package, so each caller
// brings its own branch name rather than colliding on a leftover directory.
func taskWithArtifacts(t *testing.T, branch string) (*httptest.Server, string) {
	t.Helper()
	srv := newTestServer(t, setupRepo(t), nil)
	resp, body := req(t, srv, "POST", "/task", map[string]any{
		"repo": "web-app", "branch": branch, "title": "media",
	})
	if resp.StatusCode >= 300 {
		t.Fatalf("create task: %d %v", resp.StatusCode, body)
	}
	wt, _ := body["worktreePath"].(string)
	if wt == "" {
		t.Fatalf("no worktree path in %v", body)
	}
	write := func(rel string, data []byte) {
		p := filepath.Join(wt, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("artifacts/chart.png", []byte("\x89PNG\r\n\x1a\npixels"))
	write("artifacts/summary.mp3", []byte("ID3audio"))
	write("artifacts/report.md", []byte("# report\n"))
	write(".orchestra/task.md", []byte("bookkeeping, not output"))
	return srv, wt
}

// A Delivery task's generated output is listed and classified the same way a
// Daily run's is — a diff calls a PNG "binary file changed", which is where
// this started.
func TestTaskArtifacts_ListsGeneratedOutput(t *testing.T) {
	srv, _ := taskWithArtifacts(t, "feat/media-list")
	resp, err := srv.Client().Get(srv.URL + "/task/artifacts?repo=web-app&branch=feat/media-list")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	json.NewDecoder(resp.Body).Decode(&out)

	kinds := map[string]string{}
	for _, a := range out.Artifacts {
		kinds[a.Path] = a.Kind
		if a.Size <= 0 {
			t.Errorf("%s has size %d", a.Path, a.Size)
		}
	}
	for path, want := range map[string]string{
		"artifacts/chart.png":   "image",
		"artifacts/summary.mp3": "audio",
		"artifacts/report.md":   "text",
	} {
		if kinds[path] != want {
			t.Errorf("%s classified as %q, want %q", path, kinds[path], want)
		}
	}
	if _, listed := kinds[".orchestra/task.md"]; listed {
		t.Errorf("Orchestra's own bookkeeping is not output and must not be listed")
	}
}

func TestTaskArtifact_ServesMediaInlineAndTextAsDownload(t *testing.T) {
	srv, _ := taskWithArtifacts(t, "feat/media-serve")
	get := func(path string) (*http.Response, []byte) {
		t.Helper()
		resp, err := srv.Client().Get(srv.URL + "/task/artifact?repo=web-app&branch=feat/media-serve&path=" + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}

	resp, body := get("artifacts/chart.png")
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("png content type = %q, want image/png", ct)
	}
	if string(body) != "\x89PNG\r\n\x1a\npixels" {
		t.Errorf("png bytes were altered in transit")
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("sniffing must stay off")
	}

	// Markdown is a gallery "text" but is not on the inline allowlist: rendering
	// agent-written markup in this origin is the thing that list exists to stop.
	resp, _ = get("artifacts/report.md")
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("md content type = %q, want a download", ct)
	}
}

func TestTaskArtifact_RefusesEscapesAndUnknownTasks(t *testing.T) {
	srv, _ := taskWithArtifacts(t, "feat/media-refuse")
	for _, q := range []string{
		"repo=web-app&branch=feat/media-refuse&path=../../etc/passwd",
		"repo=nope&branch=feat/media-refuse&path=artifacts/chart.png",
		"repo=web-app&branch=no/such/branch&path=artifacts/chart.png",
	} {
		resp, err := srv.Client().Get(srv.URL + "/task/artifact?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s returned %d, want a refusal", q, resp.StatusCode)
		}
	}
}
