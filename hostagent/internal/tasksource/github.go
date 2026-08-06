package tasksource

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// GitHub pulls issues assigned to the authenticated user, routed via the
// gateway's "/github/" service (read-only; the gateway injects the token and
// its write-authz still blocks mutations). Unlike the Daily providers this
// feeds the Delivery screen: each issue can be promoted into a git worktree.
type GitHub struct {
	name string
	gw   *GatewayClient
}

// NewGitHub builds a GitHub issue source.
func NewGitHub(name string, gw *GatewayClient) *GitHub { return &GitHub{name: name, gw: gw} }

// Name implements Source.
func (g *GitHub) Name() string { return g.name }

type ghIssue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	HTMLURL     string          `json:"html_url"`
	State       string          `json:"state"` // open | closed
	RepoURL     string          `json:"repository_url"`
	UpdatedAt   string          `json:"updated_at"`
	PullRequest json.RawMessage `json:"pull_request"` // present => it's a PR, not an issue
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// Fetch implements Source. With q.Repo set it pulls that repository's open
// issues (/repos/{owner}/{repo}/issues); otherwise it pulls the caller's
// assigned issues across all repos.
func (g *GitHub) Fetch(ctx context.Context, q Query) ([]Ticket, error) {
	path := "/github/issues?filter=assigned&state=open&per_page=50"
	if q.Repo != "" {
		path = "/github/repos/" + q.Repo + "/issues?state=open&per_page=50"
	}
	raw, err := g.gw.Do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return ParseIssues(raw, g.name, q.Since)
}

// ParseIssues decodes a GitHub issues array into Tickets, skipping PRs and any
// issue not newer than `since`. Shared by the gateway-routed source and the
// host-side GitHub App direct path. `fallbackRepo` ("owner/repo") is used when
// the API payload omits repository_url (the per-repo issues endpoint does).
func ParseIssues(raw []byte, source, since string) ([]Ticket, error) {
	return ParseIssuesForRepo(raw, source, since, "")
}

// ParseIssuesForRepo is ParseIssues with an explicit repo slug for payloads that
// lack repository_url.
func ParseIssuesForRepo(raw []byte, source, since, fallbackRepo string) ([]Ticket, error) {
	var issues []ghIssue
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(issues))
	for _, is := range issues {
		if len(is.PullRequest) > 0 {
			continue // issues only, not PRs
		}
		if since != "" && is.UpdatedAt <= since {
			continue
		}
		repo := repoFromAPIURL(is.RepoURL)
		if repo == "" {
			repo = fallbackRepo
		}
		labels := make([]string, 0, len(is.Labels))
		for _, l := range is.Labels {
			labels = append(labels, l.Name)
		}
		item, _ := json.Marshal(is)
		out = append(out, Ticket{
			ID:        "github:" + repo + "#" + strconv.Itoa(is.Number),
			Source:    source,
			Title:     is.Title,
			Body:      is.Body,
			URL:       is.HTMLURL,
			State:     is.State,
			Repo:      repo,
			Labels:    labels,
			UpdatedAt: is.UpdatedAt,
			Raw:       item,
		})
	}
	return out, nil
}

// repoFromAPIURL turns "https://api.github.com/repos/owner/repo" into "owner/repo".
func repoFromAPIURL(u string) string {
	i := strings.Index(u, "/repos/")
	if i < 0 {
		return ""
	}
	return strings.TrimPrefix(u[i:], "/repos/")
}
