package api

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeGitHub records what the handler asked GitHub for and replays canned
// responses, so the PR flow can be exercised without network or credentials.
type fakeGitHub struct {
	postPath   string
	postBody   map[string]any
	postStatus int
	postResp   string
	getResp    string
	tokenErr   error
}

func (f *fakeGitHub) InstallationTokenFor(context.Context, string, string) (string, error) {
	if f.tokenErr != nil {
		return "", f.tokenErr
	}
	return "ghs_faketoken", nil
}

func (f *fakeGitHub) Post(_ context.Context, _, _, apiPath string, body any) ([]byte, int, error) {
	f.postPath = apiPath
	raw, _ := json.Marshal(body)
	_ = json.Unmarshal(raw, &f.postBody)
	return []byte(f.postResp), f.postStatus, nil
}

func (f *fakeGitHub) Get(context.Context, string, string, string) ([]byte, error) {
	return []byte(f.getResp), nil
}

// GitHub answers 422 both for "a pull request already exists" and for genuinely
// invalid input. Re-opening a task should be idempotent, so an existing PR is
// returned rather than surfaced as an error.
func TestExistingPullRequestIsReturnedNotAnError(t *testing.T) {
	s := New(&Config{NoSeed: true})
	f := &fakeGitHub{getResp: `[{"number":7,"html_url":"https://github.com/o/r/pull/7"}]`}

	pr, found := s.findOpenPR(context.Background(), f, "o", "r", "feat/x", "main")
	if !found {
		t.Fatal("existing open PR not found")
	}
	if pr.Number != 7 || pr.URL != "https://github.com/o/r/pull/7" {
		t.Errorf("pr = %+v", pr)
	}
	if pr.Created {
		t.Error("Created must be false for a pre-existing PR")
	}
}

// A 422 with no matching open PR is a real validation failure and must not be
// reported as success.
func TestNoExistingPullRequestIsNotFound(t *testing.T) {
	s := New(&Config{NoSeed: true})
	if _, found := s.findOpenPR(context.Background(), &fakeGitHub{getResp: `[]`}, "o", "r", "feat/x", "main"); found {
		t.Error("empty list reported as an existing PR")
	}
}

func TestGitHubMessageExtraction(t *testing.T) {
	cases := []struct{ body, want string }{
		{`{"message":"Validation Failed","errors":[{"message":"A pull request already exists for o:feat/x."}]}`,
			"Validation Failed: A pull request already exists for o:feat/x."},
		{`{"message":"Resource not accessible by integration"}`, "Resource not accessible by integration"},
		{`not json at all`, "not json at all"},
	}
	for _, c := range cases {
		if got := githubMessage([]byte(c.body)); got != c.want {
			t.Errorf("githubMessage(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

func TestSplitSlug(t *testing.T) {
	for _, c := range []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"cancan007/Thoroughbred-Management-App", "cancan007", "Thoroughbred-Management-App", true},
		{"owner/repo.git", "owner", "repo", true},
		{" owner/repo ", "owner", "repo", true},
		{"norepo", "", "", false},
		{"/repo", "", "", false},
		{"owner/", "", "", false},
	} {
		owner, repo, ok := splitSlug(c.in)
		if ok != c.ok || owner != c.owner || repo != c.repo {
			t.Errorf("splitSlug(%q) = (%q,%q,%v), want (%q,%q,%v)", c.in, owner, repo, ok, c.owner, c.repo, c.ok)
		}
	}
}

// Guard rails that do not need GitHub at all.
func TestPullRequestValidation(t *testing.T) {
	repo := setupRepo(t)
	srv := newTestServer(t, repo, nil)
	defer srv.Close()

	// Unknown repo.
	if resp, _ := req(t, srv, "POST", "/task/pr", map[string]any{"repo": "nope", "branch": "b"}); resp.StatusCode != 404 {
		t.Errorf("unknown repo = %d, want 404", resp.StatusCode)
	}
	// Missing branch.
	if resp, _ := req(t, srv, "POST", "/task/pr", map[string]any{"repo": "web-app"}); resp.StatusCode != 400 {
		t.Errorf("missing branch = %d, want 400", resp.StatusCode)
	}
	// Branch == base is a no-op PR.
	if resp, _ := req(t, srv, "POST", "/task/pr", map[string]any{"repo": "web-app", "branch": "main"}); resp.StatusCode != 400 {
		t.Errorf("branch==base = %d, want 400", resp.StatusCode)
	}
	// No GitHub App configured: refuse before touching git, with an actionable
	// message rather than a push failure.
	resp, body := req(t, srv, "POST", "/task/pr", map[string]any{"repo": "web-app", "branch": "feat/x"})
	if resp.StatusCode != 409 {
		t.Fatalf("no GitHub App = %d, want 409", resp.StatusCode)
	}
	if msg, _ := body["error"].(string); msg == "" {
		t.Error("expected an explanatory error")
	}
}
