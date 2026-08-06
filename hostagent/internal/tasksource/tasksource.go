// Package tasksource models the Daily screen's pull-based ingest from external
// systems of record (Jira, Trello, Notion, ...). Each provider implements
// Source; adapters fetch through the security gateway (never direct egress), so
// they hold no credentials — the gateway injects them. Delivery's git-backed
// tasks are handled separately (they are worktree/PR based); this package is
// only for the "pull a ticket from somewhere else" flow.
package tasksource

import (
	"context"
	"encoding/json"
	"sort"
)

// Ticket is a provider-agnostic ticket. Query-friendly fields are promoted to
// columns by the store; the source-native payload is preserved in Raw (stored
// as a JSON column) so new fields need no schema change.
type Ticket struct {
	ID        string          `json:"id"`     // stable, source-qualified, e.g. "jira:PROJ-42"
	Source    string          `json:"source"` // "jira" | "trello" | "notion" | ...
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	URL       string          `json:"url"`
	State     string          `json:"state"` // open | in_progress | closed
	Repo      string          `json:"repo"`  // optional link to a Delivery repo ("owner/repo")
	Branch    string          `json:"branch"`
	Labels    []string        `json:"labels"`
	UpdatedAt string          `json:"updatedAt"` // RFC3339; also the incremental-pull cursor
	Raw       json.RawMessage `json:"-"`
}

// Query narrows a Fetch. Since is an RFC3339 cursor for incremental pulls: an
// adapter should return only tickets updated after it.
type Query struct {
	Assignee string
	State    string
	Since    string
	Limit    int
	// Repo, when set ("owner/repo"), scopes the fetch to a single repository
	// (GitHub: /repos/{owner}/{repo}/issues) instead of the assignee-wide feed.
	Repo string
}

// Source is one external ticket provider.
type Source interface {
	Name() string
	Fetch(ctx context.Context, q Query) ([]Ticket, error)
}

// Registry holds the configured sources by name.
type Registry struct{ m map[string]Source }

// NewRegistry builds a registry, ignoring nil sources.
func NewRegistry(sources ...Source) *Registry {
	r := &Registry{m: map[string]Source{}}
	for _, s := range sources {
		if s != nil {
			r.m[s.Name()] = s
		}
	}
	return r
}

// Get looks up a source by name.
func (r *Registry) Get(name string) (Source, bool) { s, ok := r.m[name]; return s, ok }

// Names returns the registered source names, sorted.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
