package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// setupRepo creates a repo on main with one committed file.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "-c", "init.defaultBranch=main", "init")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	// Repo-local identity so the server-side merge (which runs git without the
	// helper's env) has a committer even on machines with no global git config.
	gitCmd(t, dir, "config", "user.email", "t@e.com")
	gitCmd(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "app.ts"), []byte("export const version = 1;\n"), 0o644)
	gitCmd(t, dir, "add", ".")
	gitCmd(t, dir, "commit", "-m", "init")
	return dir
}

func newTestServer(t *testing.T, repoPath string, ci []string) *httptest.Server {
	t.Helper()
	cfg := &Config{Repos: []Repo{{Name: "web-app", Path: repoPath, Target: "main", CICommand: ci}}}
	return httptest.NewServer(New(cfg).Handler())
}

func req(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		b, _ := json.Marshal(body)
		r, err = http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
	} else {
		r, err = http.NewRequest(method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	json.NewDecoder(resp.Body).Decode(&decoded)
	resp.Body.Close()
	return resp, decoded
}

func TestWorktreeReviewFlow(t *testing.T) {
	repo := setupRepo(t)
	srv := newTestServer(t, repo, []string{"true"}) // CI always passes
	defer srv.Close()

	// 1. create a worktree/branch off main
	resp, body := req(t, srv, "POST", "/task", map[string]string{"repo": "web-app", "branch": "feat/x", "base": "main"})
	if resp.StatusCode != 201 {
		t.Fatalf("create: status %d (%v)", resp.StatusCode, body)
	}
	wt, _ := body["worktreePath"].(string)
	if wt == "" {
		t.Fatal("no worktreePath returned")
	}
	defer os.RemoveAll(wt)

	// 2. make a real change in the worktree and commit it
	os.WriteFile(filepath.Join(wt, "app.ts"), []byte("export const version = 2;\nexport const name = 'x';\n"), 0o644)
	gitCmd(t, wt, "add", ".")
	gitCmd(t, wt, "commit", "-m", "change")

	// 3. tasks list reflects the change
	resp, body = req(t, srv, "GET", "/tasks", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("tasks: status %d", resp.StatusCode)
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (%v)", len(tasks), body)
	}
	task := tasks[0].(map[string]any)
	if task["branch"] != "feat/x" {
		t.Errorf("branch = %v", task["branch"])
	}
	if task["additions"].(float64) < 1 || task["files"].(float64) != 1 {
		t.Errorf("stats add=%v files=%v", task["additions"], task["files"])
	}

	// 4. real structured diff
	resp, body = req(t, srv, "GET", "/task/diff?repo=web-app&branch=feat/x", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("diff: status %d", resp.StatusCode)
	}
	files, _ := body["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("diff files = %d, want 1", len(files))
	}
	if files[0].(map[string]any)["path"] != "app.ts" {
		t.Errorf("diff path = %v", files[0].(map[string]any)["path"])
	}

	// 5. worktree file content
	resp, body = req(t, srv, "GET", "/task/file?repo=web-app&branch=feat/x&path=app.ts", nil)
	if resp.StatusCode != 200 || !strings.Contains(body["content"].(string), "version = 2") {
		t.Errorf("file content wrong: status %d body %v", resp.StatusCode, body)
	}

	// 6. merge is gated on CI
	resp, _ = req(t, srv, "POST", "/task/merge", map[string]string{"repo": "web-app", "branch": "feat/x"})
	if resp.StatusCode != 409 {
		t.Errorf("merge before CI: status %d, want 409", resp.StatusCode)
	}

	// 7. run CI -> passes
	resp, body = req(t, srv, "POST", "/task/ci", map[string]string{"repo": "web-app", "branch": "feat/x"})
	if resp.StatusCode != 200 || body["status"] != "passed" {
		t.Fatalf("ci: status %d body %v", resp.StatusCode, body)
	}

	// 8. merge now succeeds and main contains the change
	resp, body = req(t, srv, "POST", "/task/merge", map[string]string{"repo": "web-app", "branch": "feat/x"})
	if resp.StatusCode != 200 {
		t.Fatalf("merge: status %d body %v", resp.StatusCode, body)
	}
	merged := gitCmd(t, repo, "show", "main:app.ts")
	if !strings.Contains(merged, "version = 2") {
		t.Errorf("main not updated after merge: %q", merged)
	}
}

func TestCIFailBlocksMerge(t *testing.T) {
	repo := setupRepo(t)
	srv := newTestServer(t, repo, []string{"false"}) // CI always fails
	defer srv.Close()

	resp, body := req(t, srv, "POST", "/task", map[string]string{"repo": "web-app", "branch": "feat/y", "base": "main"})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}
	wt := body["worktreePath"].(string)
	defer os.RemoveAll(wt)
	os.WriteFile(filepath.Join(wt, "app.ts"), []byte("broken\n"), 0o644)
	gitCmd(t, wt, "add", ".")
	gitCmd(t, wt, "commit", "-m", "b")

	resp, body = req(t, srv, "POST", "/task/ci", map[string]string{"repo": "web-app", "branch": "feat/y"})
	if body["status"] != "failed" {
		t.Errorf("ci status = %v, want failed", body["status"])
	}
	resp, _ = req(t, srv, "POST", "/task/merge", map[string]string{"repo": "web-app", "branch": "feat/y"})
	if resp.StatusCode != 409 {
		t.Errorf("merge after failed CI: status %d, want 409", resp.StatusCode)
	}
}

func TestWorkspaceFileWriteFlow(t *testing.T) {
	repo := setupRepo(t)
	srv := newTestServer(t, repo, []string{"true"}) // CI always passes
	defer srv.Close()

	resp, body := req(t, srv, "POST", "/task", map[string]string{"repo": "web-app", "branch": "feat/edit", "base": "main"})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}
	wt := body["worktreePath"].(string)
	defer os.RemoveAll(wt)

	// pass CI so the gate is open, then verify an edit reopens it
	req(t, srv, "POST", "/task/ci", map[string]string{"repo": "web-app", "branch": "feat/edit"})

	// list the worktree files -> committed app.ts is present
	resp, body = req(t, srv, "GET", "/task/files?repo=web-app&branch=feat/edit", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("files: status %d", resp.StatusCode)
	}
	files, _ := body["files"].([]any)
	found := false
	for _, f := range files {
		if f == "app.ts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("files list missing app.ts: %v", files)
	}

	// write a new file into a nested path (parent dirs auto-created)
	resp, body = req(t, srv, "POST", "/task/file", map[string]string{"repo": "web-app", "branch": "feat/edit", "path": "src/new.ts", "content": "export const y = 2;\n"})
	if resp.StatusCode != 200 || body["saved"] != "src/new.ts" {
		t.Fatalf("write: status %d body %v", resp.StatusCode, body)
	}

	// the edit lands on disk and reading it back returns the new content
	resp, body = req(t, srv, "GET", "/task/file?repo=web-app&branch=feat/edit&path=src/new.ts", nil)
	if resp.StatusCode != 200 || !strings.Contains(body["content"].(string), "y = 2") {
		t.Fatalf("read-back: status %d body %v", resp.StatusCode, body)
	}

	// the edit reset the CI gate -> merge is blocked again
	resp, _ = req(t, srv, "POST", "/task/merge", map[string]string{"repo": "web-app", "branch": "feat/edit"})
	if resp.StatusCode != 409 {
		t.Errorf("merge after edit: status %d, want 409 (CI must re-run)", resp.StatusCode)
	}

	// path traversal is rejected
	resp, _ = req(t, srv, "POST", "/task/file", map[string]string{"repo": "web-app", "branch": "feat/edit", "path": "../escape.ts", "content": "x"})
	if resp.StatusCode == 200 {
		t.Errorf("path traversal accepted, want rejection")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(wt), "escape.ts")); err == nil {
		t.Errorf("traversal wrote outside the worktree")
	}
}

func TestUnknownRepo(t *testing.T) {
	repo := setupRepo(t)
	srv := newTestServer(t, repo, nil)
	defer srv.Close()
	resp, _ := req(t, srv, "GET", "/task/diff?repo=nope&branch=x", nil)
	if resp.StatusCode != 404 {
		t.Errorf("unknown repo: status %d, want 404", resp.StatusCode)
	}
}
