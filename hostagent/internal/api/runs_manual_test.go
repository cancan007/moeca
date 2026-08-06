package api

import (
	"net/http/httptest"
	"testing"
)

func TestManualRunRecordAndList(t *testing.T) {
	srv := httptest.NewServer(New(&Config{NoSeed: true}).Handler())
	defer srv.Close()

	_, body := req(t, srv, "GET", "/runs", nil)
	if rs, _ := body["runs"].([]any); len(rs) != 0 {
		t.Fatalf("expected empty, got %v", rs)
	}
	resp, body := req(t, srv, "POST", "/runs", map[string]any{
		"name": "WEB-241", "repo": "web-app", "branch": "feat/x", "task": "fix",
		"template": "Graph — Frontend", "templateRef": "static:g1", "runId": "r9",
	})
	if resp.StatusCode != 201 {
		t.Fatalf("record: %d %v", resp.StatusCode, body)
	}
	_, body = req(t, srv, "GET", "/runs", nil)
	rs, _ := body["runs"].([]any)
	if len(rs) != 1 {
		t.Fatalf("runs = %d, want 1", len(rs))
	}
	m := rs[0].(map[string]any)
	if m["repo"] != "web-app" || m["templateRef"] != "static:g1" || m["runId"] != "r9" {
		t.Fatalf("bad record: %v", m)
	}
}
