package tasksource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockGateway stands in for the security gateway: it records the request and
// returns canned upstream JSON. It also asserts the session header is present.
func mockGateway(t *testing.T, wantPath, respBody string) (*httptest.Server, *string) {
	t.Helper()
	gotPath := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Orchestra-Session") != "sess" {
			t.Errorf("missing/wrong session header: %q", r.Header.Get("X-Orchestra-Session"))
		}
		*gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, gotPath
}

func TestJiraFetchMapsIssues(t *testing.T) {
	body := `{"issues":[
	  {"key":"PROJ-42","fields":{"summary":"Fix login","updated":"2026-07-11T09:00:00.000+0000",
	    "status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}}}}]}`
	srv, gotPath := mockGateway(t, "/jira/rest/api/3/search", body)
	j := NewJira("jira", NewGatewayClient(srv.URL, "sess"))

	got, err := j.Fetch(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if *gotPath != "/jira/rest/api/3/search" {
		t.Fatalf("path = %s", *gotPath)
	}
	if len(got) != 1 || got[0].ID != "jira:PROJ-42" || got[0].Title != "Fix login" || got[0].State != "in_progress" {
		t.Fatalf("bad mapping: %+v", got)
	}
}

func TestTrelloFetchMapsCards(t *testing.T) {
	body := `[{"id":"abc","name":"Design review","url":"https://trello.com/c/abc","dateLastActivity":"2026-07-12T00:00:00Z","closed":false}]`
	srv, _ := mockGateway(t, "/trello/1/members/me/cards", body)
	tr := NewTrello("trello", NewGatewayClient(srv.URL, "sess"))

	got, err := tr.Fetch(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "trello:abc" || got[0].URL != "https://trello.com/c/abc" || got[0].State != "open" {
		t.Fatalf("bad mapping: %+v", got)
	}
}

func TestNotionFetchMapsPages(t *testing.T) {
	body := `{"results":[{"id":"page1","url":"https://notion.so/page1","last_edited_time":"2026-07-13T00:00:00Z",
	  "properties":{"Name":{"type":"title","title":[{"plain_text":"Spec draft"}]}}}]}`
	srv, gotPath := mockGateway(t, "/notion/v1/search", body)
	n := NewNotion("notion", NewGatewayClient(srv.URL, "sess"))

	got, err := n.Fetch(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if *gotPath != "/notion/v1/search" {
		t.Fatalf("path = %s", *gotPath)
	}
	if len(got) != 1 || got[0].ID != "notion:page1" || got[0].Title != "Spec draft" {
		t.Fatalf("bad mapping: %+v", got)
	}
}

func TestJiraSinceCursorFilters(t *testing.T) {
	body := `{"issues":[
	  {"key":"OLD-1","fields":{"summary":"old","updated":"2026-07-01T00:00:00.000+0000","status":{"statusCategory":{"key":"new"}}}},
	  {"key":"NEW-1","fields":{"summary":"new","updated":"2026-07-10T00:00:00.000+0000","status":{"statusCategory":{"key":"new"}}}}]}`
	srv, _ := mockGateway(t, "/jira/rest/api/3/search", body)
	j := NewJira("jira", NewGatewayClient(srv.URL, "sess"))

	got, err := j.Fetch(context.Background(), Query{Since: "2026-07-05T00:00:00.000+0000"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "jira:NEW-1" {
		t.Fatalf("Since not applied: %+v", got)
	}
}
