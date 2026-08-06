package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// groupedIndex builds an index over two local sources with different group
// labels plus one untagged source, so a search can be checked for what it does
// and does not reach.
func groupedIndex(t *testing.T) *Index {
	t.Helper()
	gw := mockGateway(t)
	t.Cleanup(gw.Close)

	mk := func(name, body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	idx := New(Config{
		Sources: []SourceSpec{
			{Kind: KindLocal, Root: mk("a.md", "alpha secret"), Scope: ScopeProject, Groups: []string{"team-a"}},
			{Kind: KindLocal, Root: mk("b.md", "alpha other"), Scope: ScopeProject, Groups: []string{"team-b", "shared"}},
			{Kind: KindLocal, Root: mk("c.md", "alpha untagged"), Scope: ScopeProject},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return idx
}

func sourcesOf(res []Result) map[string]bool {
	out := map[string]bool{}
	for _, r := range res {
		out[r.Source] = true
	}
	return out
}

// A nil filter is "no policy" — the host's own unscoped calls must keep seeing
// the whole index, including sources that carry no groups at all.
func TestSearchWithoutPolicySeesEverything(t *testing.T) {
	res, err := groupedIndex(t).Search(context.Background(), "alpha", 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := sourcesOf(res)
	for _, want := range []string{"a.md", "b.md", "c.md"} {
		if !got[want] {
			t.Errorf("unscoped search missed %s (got %v)", want, got)
		}
	}
}

// The core guarantee: a scoped search reaches its own groups and nothing else.
func TestSearchIsLimitedToPermittedGroups(t *testing.T) {
	res, err := groupedIndex(t).Search(context.Background(), "alpha", 10, NewGroupFilter([]string{"team-a"}))
	if err != nil {
		t.Fatal(err)
	}
	got := sourcesOf(res)
	if !got["a.md"] {
		t.Errorf("permitted source missing (got %v)", got)
	}
	if got["b.md"] {
		t.Error("leaked a source belonging to another group")
	}
	if got["c.md"] {
		t.Error("leaked an untagged source into a scoped search")
	}
}

// A source with several groups is reachable through any one of them.
func TestAnyMatchingGroupPermits(t *testing.T) {
	res, err := groupedIndex(t).Search(context.Background(), "alpha", 10, NewGroupFilter([]string{"shared"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := sourcesOf(res); !got["b.md"] || len(got) != 1 {
		t.Errorf("got %v, want only b.md", got)
	}
}

// A run with no groups must see nothing. This is the case an empty header
// produces, and folding it into "no policy" would hand an unprivileged run the
// entire index — the exact inversion this filter exists to prevent.
func TestEmptyGroupSetSeesNothing(t *testing.T) {
	res, err := groupedIndex(t).Search(context.Background(), "alpha", 10, NewGroupFilter(nil))
	if err != nil {
		t.Fatalf("an empty group set is a permission outcome, not an error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("got %d results, want none", len(res))
	}
}

// k applies to what the caller may see. Were it applied before filtering, a
// scoped search would return short lists whose length betrays how many hidden
// chunks outranked the visible ones.
func TestTopKCountsOnlyPermittedChunks(t *testing.T) {
	res, err := groupedIndex(t).Search(context.Background(), "alpha", 1, NewGroupFilter([]string{"team-a", "team-b"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want exactly k=1", len(res))
	}
	if s := res[0].Source; s != "a.md" && s != "b.md" {
		t.Errorf("result came from %s, outside the permitted groups", s)
	}
}

// Results carry their groups so a caller can show why a chunk was reachable.
func TestResultsCarryTheirGroups(t *testing.T) {
	res, err := groupedIndex(t).Search(context.Background(), "alpha", 10, NewGroupFilter([]string{"team-a"}))
	if err != nil || len(res) == 0 {
		t.Fatalf("search: %v (%d results)", err, len(res))
	}
	if len(res[0].Groups) != 1 || res[0].Groups[0] != "team-a" {
		t.Errorf("Groups = %v, want [team-a]", res[0].Groups)
	}
}

func TestNormalizeGroups(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"  a ", "a", ""}, []string{"a"}},
		{[]string{" ", ""}, nil},
		{[]string{"a", "b"}, []string{"a", "b"}},
	} {
		got := normalizeGroups(c.in)
		if len(got) != len(c.want) {
			t.Errorf("normalizeGroups(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for k := range got {
			if got[k] != c.want[k] {
				t.Errorf("normalizeGroups(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}

// Matching is exact after trimming: a near miss denies rather than grants.
func TestGroupMatchingIsExact(t *testing.T) {
	f := NewGroupFilter([]string{" team-a "})
	if !f.permits([]string{"team-a"}, false) {
		t.Error("surrounding whitespace should not change a group's identity")
	}
	if f.permits([]string{"Team-A"}, false) {
		t.Error("case differences must deny, not grant")
	}
	if f.permits(nil, false) {
		t.Error("an untagged chunk must not pass a policy")
	}
	if !(*GroupFilter)(nil).permits(nil, false) {
		t.Error("a nil filter states no policy and must permit")
	}
}

// Global knowledge — a handbook, a glossary, a coding standard — is everyone's.
// Requiring a group membership per team to read it would mean re-granting the
// same document to every group that ever exists.
func TestGlobalScopeBypassesTheGroupFilter(t *testing.T) {
	f := NewGroupFilter([]string{"team-a"})
	if !f.permits(nil, true) {
		t.Error("a globally-scoped chunk must be readable under any policy")
	}
	if !f.permits([]string{"team-z"}, true) {
		t.Error("global must win over a non-matching group as well")
	}
	// The narrow scopes are unaffected.
	if f.permits([]string{"team-b"}, false) {
		t.Error("a non-global chunk must still be filtered by group")
	}
}

// End to end: a globally-scoped source is reachable by a run that was granted
// groups it does not carry, while the narrowly-scoped ones around it are not.
// This is the whole point of the scope — the group filter is the mechanism, and
// global is the declared exemption from it.
func TestGlobalSourceIsReachableUnderAPolicy(t *testing.T) {
	gw := mockGateway(t)
	t.Cleanup(gw.Close)

	mk := func(name, body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	idx := New(Config{
		Sources: []SourceSpec{
			{Kind: KindLocal, Root: mk("handbook.md", "alpha handbook"), Scope: ScopeGlobal},
			{Kind: KindLocal, Root: mk("team.md", "alpha team notes"), Scope: ScopeProject, Groups: []string{"team-a"}},
			{Kind: KindLocal, Root: mk("secret.md", "alpha secret"), Scope: ScopeProject, Groups: []string{"team-b"}},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	res, err := idx.Search(context.Background(), "alpha", 10, NewGroupFilter([]string{"team-a"}))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got := sourcesOf(res)
	if !got["handbook.md"] {
		t.Error("a globally-scoped source must be reachable whatever groups the run holds")
	}
	if !got["team.md"] {
		t.Error("a permitted group's source went missing")
	}
	if got["secret.md"] {
		t.Error("another group's project-scoped source leaked")
	}

	// A run granted nothing still sees the global source, and only that.
	res, err = idx.Search(context.Background(), "alpha", 10, NewGroupFilter(nil))
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	got = sourcesOf(res)
	if !got["handbook.md"] || len(got) != 1 {
		t.Errorf("empty policy reached %v, want only the global source", got)
	}
}
