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

// Global knowledge — a handbook, a glossary, a coding standard — is everyone's
// for as long as nobody has said who it is for. Requiring a group membership
// per team to read it would mean re-granting the same document to every group
// that ever exists.
func TestUnassignedGlobalScopeBypassesTheGroupFilter(t *testing.T) {
	f := NewGroupFilter([]string{"team-a"})
	if !f.permits(nil, true) {
		t.Error("a globally-scoped chunk in no group must be readable under any policy")
	}
	// The narrow scopes are unaffected.
	if f.permits([]string{"team-b"}, false) {
		t.Error("a non-global chunk must still be filtered by group")
	}
}

// Putting a source into a group is what narrows it, and it is the only gesture
// that does: the scope is not declared a second time anywhere, it is read off
// membership. A global source that has been assigned is therefore no longer
// everyone's, and is filtered like any other.
func TestAssigningAGlobalSourceNarrowsIt(t *testing.T) {
	f := NewGroupFilter([]string{"team-a"})
	if f.permits([]string{"team-z"}, true) {
		t.Error("a global chunk assigned to a group it does not grant must be denied")
	}
	if !f.permits([]string{"team-a"}, true) {
		t.Error("a global chunk assigned to a granted group must be permitted")
	}
	// An unscoped caller is unaffected: no policy still means no filtering, so
	// assigning sources cannot break the runs that never asked for a scope.
	if !(*GroupFilter)(nil).permits([]string{"team-z"}, true) {
		t.Error("a nil filter must still permit an assigned global chunk")
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

// End to end, and the gesture the whole design now rests on: a source starts as
// everyone's, and putting it into a group on the Knowledge screen is what makes
// the filter apply to it. Nothing else is declared, and nothing needs a rebuild
// — SetGroups re-tags what is already there.
func TestAssignmentNarrowsAGlobalSourceAndRestoresIt(t *testing.T) {
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
			{Kind: KindLocal, Root: mk("handbook.md", "alpha handbook")},
			{Kind: KindLocal, Root: mk("payroll.md", "alpha payroll")},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The host pushes a mapping even when it assigns nothing — that is what an
	// empty graph looks like, and it is the difference between "nobody is in a
	// group" and "nobody has said anything yet". Before assignment both sources
	// are therefore global and every policy reaches them.
	idx.SetGroups(map[string][]string{})
	got := sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"team-a"})))
	if !got["handbook.md"] || !got["payroll.md"] {
		t.Fatalf("unassigned sources reached %v, want both", got)
	}

	// Someone says who payroll.md is for.
	if matched := idx.SetGroups(map[string][]string{"payroll.md": {"finance"}}); matched != 1 {
		t.Fatalf("SetGroups matched %d sources, want 1", matched)
	}

	got = sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"team-a"})))
	if got["payroll.md"] {
		t.Error("an assigned source stayed reachable by a run that was not granted its group")
	}
	if !got["handbook.md"] {
		t.Error("the source nobody assigned must stay everyone's")
	}
	got = sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"finance"})))
	if !got["payroll.md"] {
		t.Error("the granted group cannot reach its own source")
	}

	// The panel must not go on calling it global while searches skip it.
	for _, s := range idx.Status().Sources {
		if s.Path == "payroll.md" && s.Scope != ScopeProject {
			t.Errorf("assigned source reports scope %q, want %q", s.Scope, ScopeProject)
		}
		if s.Path == "handbook.md" && s.Scope != ScopeGlobal {
			t.Errorf("unassigned source reports scope %q, want %q", s.Scope, ScopeGlobal)
		}
	}

	// Taking it back out widens it again. Deriving the reported scope from the
	// declared one rather than from itself is what makes this reversible.
	idx.SetGroups(map[string][]string{})
	got = sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"team-a"})))
	if !got["payroll.md"] {
		t.Error("a source removed from every group did not return to being everyone's")
	}
	for _, s := range idx.Status().Sources {
		if s.Path == "payroll.md" && s.Scope != ScopeGlobal {
			t.Errorf("unassigned source reports scope %q, want %q", s.Scope, ScopeGlobal)
		}
	}
}

// A source configured narrower than global stays narrow with no groups at all:
// membership narrows, it never widens. Otherwise clearing an assignment would
// quietly publish something a person had declared restricted.
func TestMembershipNeverWidensADeclaredScope(t *testing.T) {
	if got := effectiveScope(ScopeProject, nil); got != ScopeProject {
		t.Errorf("declared project with no groups = %q, want %q", got, ScopeProject)
	}
	if got := effectiveScope(ScopeOrganization, []string{"team-a"}); got != ScopeOrganization {
		t.Errorf("declared organization = %q, want it left alone", got)
	}
	if got := effectiveScope(ScopeGlobal, nil); got != ScopeGlobal {
		t.Errorf("declared global with no groups = %q, want %q", got, ScopeGlobal)
	}
}

func mustSearch(t *testing.T, idx *Index, q string, f *GroupFilter) []Result {
	t.Helper()
	res, err := idx.Search(context.Background(), q, 10, f)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res
}

// Membership must survive the restart that registering a knowledge source
// causes: rebinding a mount restarts this container, and an indexer that came
// back without its labels would treat every source as unclaimed — which since
// scope is read off membership means everyone's.
func TestGroupMembershipSurvivesARestart(t *testing.T) {
	gw := mockGateway(t)
	t.Cleanup(gw.Close)
	cache := t.TempDir()

	mk := func(name, body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	handbook, payroll := mk("handbook.md", "alpha handbook"), mk("payroll.md", "alpha payroll")
	cfg := Config{
		Sources: []SourceSpec{
			{Kind: KindLocal, Root: handbook},
			{Kind: KindLocal, Root: payroll},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
		CacheDir: cache,
	}

	first := New(cfg)
	if err := first.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	first.SetGroups(map[string][]string{"payroll.md": {"finance"}})

	// A new process over the same cache directory: the labels come back before
	// anything is searchable, without the Knowledge screen being opened.
	second := New(cfg)
	second.LoadGroups()
	if err := second.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := sourcesOf(mustSearch(t, second, "alpha", NewGroupFilter([]string{"team-a"})))
	if got["payroll.md"] {
		t.Error("a restarted indexer leaked an assigned source to a run that was not granted its group")
	}
	if !got["handbook.md"] {
		t.Error("the unassigned source should still be everyone's after a restart")
	}
	if !sourcesOf(mustSearch(t, second, "alpha", NewGroupFilter([]string{"finance"})))["payroll.md"] {
		t.Error("the granted group cannot reach its own source after a restart")
	}
}

// With no cache directory the mapping stays in memory, exactly as before this
// existed. Nothing should panic or write anywhere.
func TestGroupMembershipWithoutACacheDirectory(t *testing.T) {
	idx := New(Config{})
	idx.LoadGroups() // no-op
	if n := idx.SetGroups(map[string][]string{"a.md": {"g"}}); n != 0 {
		t.Errorf("SetGroups matched %d sources in an empty index, want 0", n)
	}
}

// The last resort: nothing has been pushed at all — no mapping on disk, no host
// to ask — and a scoped run arrives anyway. It must retrieve nothing rather
// than everything, because "no labels" cannot be read as "everything is
// everyone's" when scope is derived from labels.
func TestNothingPushedHidesSourcesThatAreGlobalOnlyByDefault(t *testing.T) {
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
			// Nobody said anything about this one.
			{Kind: KindLocal, Root: mk("payroll.md", "alpha payroll")},
			// Someone declared this one everyone's, in the config itself.
			{Kind: KindLocal, Root: mk("handbook.md", "alpha handbook"), Scope: ScopeGlobal},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	got := sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"team-a"})))
	if got["payroll.md"] {
		t.Error("a source global only by default was retrievable before anything was pushed")
	}
	// A declaration is a person's word and this is not the place to overrule it;
	// it is also the only way to run the indexer standalone, with no host.
	if !got["handbook.md"] {
		t.Error("a source whose config declares it global must stay reachable")
	}

	// Unscoped callers are untouched: a nil filter never consults any of this,
	// so the host's own routes still see the whole index.
	if len(sourcesOf(mustSearch(t, idx, "alpha", nil))) != 2 {
		t.Error("an unscoped search must be unaffected by the closed state")
	}

	// Reported honestly while it lasts: a panel calling it "global · default"
	// while every scoped search skips it is the same error, merely printed.
	for _, s := range idx.Status().Sources {
		if s.Path == "payroll.md" && s.Scope != ScopeProject {
			t.Errorf("hidden source reports scope %q, want %q", s.Scope, ScopeProject)
		}
	}
}

// And it is a hold, not a verdict. The moment the host speaks — even to say
// that nothing is assigned — the defaults go back to being everyone's.
func TestAPushLiftsTheClosedState(t *testing.T) {
	gw := mockGateway(t)
	t.Cleanup(gw.Close)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payroll.md"), []byte("alpha payroll"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := New(Config{
		Sources: []SourceSpec{{Kind: KindLocal, Root: dir}},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"team-a"})))["payroll.md"] {
		t.Fatal("precondition: the source should start hidden")
	}

	// An empty map is a real answer — "no group has any source" — and is what
	// the host pushes for an empty graph. It is not the same as silence.
	idx.SetGroups(map[string][]string{})
	if !sourcesOf(mustSearch(t, idx, "alpha", NewGroupFilter([]string{"team-a"})))["payroll.md"] {
		t.Error("an empty push did not lift the hold; silence and 'nothing assigned' were collapsed")
	}
	for _, s := range idx.Status().Sources {
		if s.Scope != ScopeGlobal {
			t.Errorf("after the push the source reports %q, want %q", s.Scope, ScopeGlobal)
		}
	}
}
