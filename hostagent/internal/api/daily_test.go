package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDailyPullAndList(t *testing.T) {
	srv := httptest.NewServer(New(&Config{DemoSources: true}).Handler())
	defer srv.Close()

	// the demo source is registered
	_, body := req(t, srv, "GET", "/daily/sources", nil)
	srcs, _ := body["sources"].([]any)
	if len(srcs) != 1 || srcs[0] != "demo" {
		t.Fatalf("sources = %v, want [demo]", srcs)
	}

	// pull ingests the two demo tickets
	resp, body := req(t, srv, "POST", "/daily/pull?source=demo", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("pull: status %d (%v)", resp.StatusCode, body)
	}
	if body["pulled"].(float64) != 2 {
		t.Fatalf("pulled = %v, want 2", body["pulled"])
	}

	// tickets are now persisted and listable
	_, body = req(t, srv, "GET", "/daily/tickets", nil)
	tickets, _ := body["tickets"].([]any)
	if len(tickets) != 2 {
		t.Fatalf("tickets = %d, want 2", len(tickets))
	}

	// a second pull is incremental: nothing new past the cursor
	_, body = req(t, srv, "POST", "/daily/pull?source=demo", nil)
	if body["pulled"].(float64) != 0 {
		t.Fatalf("second pull pulled = %v, want 0 (cursor advanced)", body["pulled"])
	}

	// unknown source => 404
	resp, _ = req(t, srv, "POST", "/daily/pull?source=nope", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown source status = %d, want 404", resp.StatusCode)
	}
}

func TestPromoteTicketToWorktree(t *testing.T) {
	// promote creates a worktree under a shared temp path (os.TempDir); clear any
	// leftover before and after so repeated local runs stay idempotent.
	wtDir := filepath.Join(os.TempDir(), "orchestra-wt", "web-app")
	os.RemoveAll(wtDir)
	t.Cleanup(func() { os.RemoveAll(wtDir) })
	repo := setupRepo(t)
	cfg := &Config{
		Repos:       []Repo{{Name: "web-app", Path: repo, Target: "main", CICommand: []string{"true"}}},
		DemoSources: true,
	}
	srv := httptest.NewServer(New(cfg).Handler())
	defer srv.Close()

	req(t, srv, "POST", "/daily/pull?source=demo", nil) // seed tickets

	// promote a demo ticket into the repo -> a Delivery worktree/branch
	resp, body := req(t, srv, "POST", "/daily/promote", map[string]string{"id": "demo:1", "repo": "web-app"})
	if resp.StatusCode != 201 {
		t.Fatalf("promote: status %d (%v)", resp.StatusCode, body)
	}
	if body["branch"] != "ticket/demo-1" || body["repo"] != "web-app" {
		t.Fatalf("promote returned %v", body)
	}

	// the new branch shows up as a Delivery task
	_, body = req(t, srv, "GET", "/tasks", nil)
	tasks, _ := body["tasks"].([]any)
	found := false
	for _, tk := range tasks {
		if m, ok := tk.(map[string]any); ok && m["branch"] == "ticket/demo-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("promoted branch not listed in /tasks: %v", tasks)
	}

	// unknown ticket => 404
	resp, _ = req(t, srv, "POST", "/daily/promote", map[string]string{"id": "demo:404", "repo": "web-app"})
	if resp.StatusCode != 404 {
		t.Fatalf("promote unknown ticket status = %d, want 404", resp.StatusCode)
	}
}

func TestSourceConfigPersistsAndRebuilds(t *testing.T) {
	srv := httptest.NewServer(New(&Config{}).Handler())
	defer srv.Close()

	// no sources initially
	_, body := req(t, srv, "GET", "/daily/sources", nil)
	if s, _ := body["sources"].([]any); len(s) != 0 {
		t.Fatalf("expected no sources, got %v", s)
	}
	// add a jira source -> registry rebuilds and lists it
	resp, _ := req(t, srv, "POST", "/daily/sources/config", map[string]string{"type": "jira"})
	if resp.StatusCode != 201 {
		t.Fatalf("add source: %d", resp.StatusCode)
	}
	_, body = req(t, srv, "GET", "/daily/sources", nil)
	if s, _ := body["sources"].([]any); len(s) != 1 || s[0] != "jira" {
		t.Fatalf("sources = %v, want [jira]", body["sources"])
	}
	// bad type rejected
	resp, _ = req(t, srv, "POST", "/daily/sources/config", map[string]string{"type": "slack"})
	if resp.StatusCode != 400 {
		t.Fatalf("bad type status = %d, want 400", resp.StatusCode)
	}
	// remove it
	resp, _ = req(t, srv, "DELETE", "/daily/sources/config?name=jira", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("remove source: %d", resp.StatusCode)
	}
	_, body = req(t, srv, "GET", "/daily/sources", nil)
	if s, _ := body["sources"].([]any); len(s) != 0 {
		t.Fatalf("sources after remove = %v, want []", s)
	}
}
