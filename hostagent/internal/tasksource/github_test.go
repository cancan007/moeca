package tasksource

import (
	"context"
	"testing"
)

func TestGitHubFetchMapsIssuesAndSkipsPRs(t *testing.T) {
	body := `[
	  {"number":42,"title":"Fix login","body":"b","html_url":"https://github.com/o/r/issues/42",
	   "state":"open","repository_url":"https://api.github.com/repos/o/r","updated_at":"2026-07-11T09:00:00Z",
	   "labels":[{"name":"bug"}]},
	  {"number":43,"title":"a PR","html_url":"https://github.com/o/r/pull/43","state":"open",
	   "repository_url":"https://api.github.com/repos/o/r","updated_at":"2026-07-12T00:00:00Z",
	   "pull_request":{"url":"x"}}
	]`
	srv, gotPath := mockGateway(t, "/github/issues", body)
	g := NewGitHub("github", NewGatewayClient(srv.URL, "sess"))

	got, err := g.Fetch(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if *gotPath != "/github/issues" {
		t.Fatalf("path = %s", *gotPath)
	}
	if len(got) != 1 { // the PR is skipped
		t.Fatalf("got %d tickets, want 1 (PR skipped): %+v", len(got), got)
	}
	tk := got[0]
	if tk.ID != "github:o/r#42" || tk.Repo != "o/r" || tk.Title != "Fix login" || tk.State != "open" {
		t.Fatalf("bad mapping: %+v", tk)
	}
	if len(tk.Labels) != 1 || tk.Labels[0] != "bug" {
		t.Fatalf("labels: %v", tk.Labels)
	}
}

func TestRepoFromAPIURL(t *testing.T) {
	if got := repoFromAPIURL("https://api.github.com/repos/owner/repo"); got != "owner/repo" {
		t.Fatalf("got %q", got)
	}
	if got := repoFromAPIURL("garbage"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
