package api

import (
	"net/http/httptest"
	"testing"
)

func TestTaskMetaGoalMilestones(t *testing.T) {
	srv := httptest.NewServer(New(&Config{NoSeed: true}).Handler())
	defer srv.Close()

	// empty initially
	_, body := req(t, srv, "GET", "/task/meta?repo=web-app&branch=feat/x", nil)
	if ms, _ := body["milestones"].([]any); len(ms) != 0 {
		t.Fatalf("expected empty, got %v", body)
	}
	// goal without milestone => 400
	resp, _ := req(t, srv, "POST", "/task/meta", map[string]any{"repo": "web-app", "branch": "feat/x", "goal": "ship v2"})
	if resp.StatusCode != 400 {
		t.Fatalf("goal w/o milestone: %d", resp.StatusCode)
	}
	// goal + milestone => 200, persisted
	resp, _ = req(t, srv, "POST", "/task/meta", map[string]any{
		"repo": "web-app", "branch": "feat/x", "goal": "ship v2",
		"milestones": []map[string]any{{"title": "m1", "done": false}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("set: %d", resp.StatusCode)
	}
	_, body = req(t, srv, "GET", "/task/meta?repo=web-app&branch=feat/x", nil)
	if body["goal"] != "ship v2" {
		t.Fatalf("goal not persisted: %v", body)
	}
	if ms, _ := body["milestones"].([]any); len(ms) != 1 {
		t.Fatalf("milestones: %v", body["milestones"])
	}
}

// The template picker and the goal editor write the same row independently, so
// a partial update must leave the fields it does not mention alone.
func TestTaskMetaPartialUpdatesDoNotClobber(t *testing.T) {
	srv := httptest.NewServer(New(&Config{NoSeed: true}).Handler())
	defer srv.Close()

	const q = "/task/meta?repo=web-app&branch=feat/x"

	// goal + milestone first
	resp, _ := req(t, srv, "POST", "/task/meta", map[string]any{
		"repo": "web-app", "branch": "feat/x", "goal": "ship v2",
		"milestones": []map[string]any{{"title": "m1", "done": false}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("set goal: %d", resp.StatusCode)
	}

	// assigning a template must not wipe the goal/milestones
	resp, _ = req(t, srv, "POST", "/task/meta", map[string]any{
		"repo": "web-app", "branch": "feat/x", "template": "impl",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("set template: %d", resp.StatusCode)
	}
	_, body := req(t, srv, "GET", q, nil)
	if body["goal"] != "ship v2" {
		t.Errorf("goal clobbered by template update: %v", body)
	}
	if ms, _ := body["milestones"].([]any); len(ms) != 1 {
		t.Errorf("milestones clobbered by template update: %v", body["milestones"])
	}
	if body["template"] != "impl" {
		t.Errorf("template = %v, want impl", body["template"])
	}

	// and editing the goal must not wipe the template
	resp, _ = req(t, srv, "POST", "/task/meta", map[string]any{
		"repo": "web-app", "branch": "feat/x", "goal": "ship v3",
		"milestones": []map[string]any{{"title": "m1", "done": true}},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("update goal: %d", resp.StatusCode)
	}
	_, body = req(t, srv, "GET", q, nil)
	if body["template"] != "impl" {
		t.Errorf("template clobbered by goal update: %v", body)
	}
	if body["goal"] != "ship v3" {
		t.Errorf("goal = %v, want ship v3", body)
	}
}
