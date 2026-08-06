package store

import (
	"strings"
	"testing"
)

func openKB(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A group id becomes its permission tag, so it must survive a rename. If it
// changed, editing a display name would silently revoke every task holding the
// old tag.
func TestGroupIDIsStableAcrossRename(t *testing.T) {
	s := openKB(t)
	g, err := s.AddKnowledgeGroup("決済サービス", "var(--ac)", "k.saito", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateKnowledgeGroup(g.ID, "決済ドメイン", "var(--ac)", "k.saito", "改称"); err != nil {
		t.Fatal(err)
	}
	got, err := s.KnowledgeGroups()
	if err != nil || len(got) != 1 {
		t.Fatalf("groups: %v (%d)", err, len(got))
	}
	if got[0].ID != g.ID {
		t.Errorf("id changed on rename: %q -> %q", g.ID, got[0].ID)
	}
	if got[0].Name != "決済ドメイン" {
		t.Errorf("name = %q, want the new one", got[0].Name)
	}
}

// Two groups may share a display name; they may not share a tag, or one would
// inherit the other's permissions.
func TestDuplicateNamesGetDistinctIDs(t *testing.T) {
	s := openKB(t)
	a, _ := s.AddKnowledgeGroup("Release", "", "", "")
	b, err := s.AddKnowledgeGroup("Release", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("both groups got id %q", a.ID)
	}
}

// Japanese names reduce to nothing under slugification, so they must still
// produce a usable, distinct id rather than an empty one.
func TestJapaneseNamesProduceDistinctIDs(t *testing.T) {
	s := openKB(t)
	a, _ := s.AddKnowledgeGroup("認証", "", "", "")
	b, _ := s.AddKnowledgeGroup("監視", "", "", "")
	for _, id := range []string{a.ID, b.ID} {
		if id == "grp-" || !strings.HasPrefix(id, "grp-") {
			t.Errorf("unusable id %q", id)
		}
	}
	if a.ID == b.ID {
		t.Errorf("different names collided on %q", a.ID)
	}
}

// Deleting a group must take its memberships and relations with it. Without
// foreign keys enforced these rows would linger, and a stale relation would
// still be traversed during retrieval.
func TestDeletingGroupCascades(t *testing.T) {
	s := openKB(t)
	org, _ := s.AddKnowledgeOrg("Acme")
	prj, _ := s.AddKnowledgeProject("決済基盤", org.ID)
	a, _ := s.AddKnowledgeGroup("A", "", "", "")
	b, _ := s.AddKnowledgeGroup("B", "", "", "")
	if err := s.SetKnowledgeGroupProjects(a.ID, []string{prj.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetKnowledgeGroupSources(a.ID, []string{"docs/a.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddKnowledgeRelation(a.ID, b.ID, "requires"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DeleteKnowledgeGroup(a.ID); err != nil {
		t.Fatal(err)
	}
	rels, _ := s.KnowledgeRelations()
	if len(rels) != 0 {
		t.Errorf("relation survived its endpoint's deletion: %+v", rels)
	}
	m, _ := s.GroupsForSources()
	if len(m) != 0 {
		t.Errorf("membership survived group deletion: %+v", m)
	}
}

// Deleting an organization removes its projects, and that in turn unlinks the
// groups those projects had claimed.
func TestDeletingOrgCascadesToProjectLinks(t *testing.T) {
	s := openKB(t)
	org, _ := s.AddKnowledgeOrg("Acme")
	prj, _ := s.AddKnowledgeProject("決済基盤", org.ID)
	g, _ := s.AddKnowledgeGroup("A", "", "", "")
	s.SetKnowledgeGroupProjects(g.ID, []string{prj.ID})

	if _, err := s.DeleteKnowledgeOrg(org.ID); err != nil {
		t.Fatal(err)
	}
	if ps, _ := s.KnowledgeProjects(); len(ps) != 0 {
		t.Errorf("projects survived their org: %+v", ps)
	}
	gs, _ := s.KnowledgeGroups()
	if len(gs) != 1 {
		t.Fatalf("group count = %d, want 1 (groups outlive projects)", len(gs))
	}
	if len(gs[0].Projects) != 0 {
		t.Errorf("group still linked to a deleted project: %v", gs[0].Projects)
	}
}

// Membership is edited as a set, so replacing must drop what is no longer
// selected — a stale source would stay readable by the group.
func TestSetSourcesReplacesRatherThanAdds(t *testing.T) {
	s := openKB(t)
	g, _ := s.AddKnowledgeGroup("A", "", "", "")
	s.SetKnowledgeGroupSources(g.ID, []string{"a.md", "b.md"})
	s.SetKnowledgeGroupSources(g.ID, []string{"b.md", "c.md"})

	gs, _ := s.KnowledgeGroups()
	got := strings.Join(gs[0].Sources, ",")
	if got != "b.md,c.md" {
		t.Errorf("sources = %q, want b.md,c.md", got)
	}
}

// Duplicates and blanks in a submitted set must not become rows; the primary
// key would reject the second identical insert and fail the whole edit.
func TestSetSourcesIgnoresBlanksAndDuplicates(t *testing.T) {
	s := openKB(t)
	g, _ := s.AddKnowledgeGroup("A", "", "", "")
	if err := s.SetKnowledgeGroupSources(g.ID, []string{"a.md", " a.md ", "", "  "}); err != nil {
		t.Fatalf("a set with duplicates must not fail the edit: %v", err)
	}
	gs, _ := s.KnowledgeGroups()
	if len(gs[0].Sources) != 1 || gs[0].Sources[0] != "a.md" {
		t.Errorf("sources = %v, want [a.md]", gs[0].Sources)
	}
}

// This map is what tags chunks at index time, so it is the direct input to the
// permission filter.
func TestGroupsForSources(t *testing.T) {
	s := openKB(t)
	a, _ := s.AddKnowledgeGroup("A", "", "", "")
	b, _ := s.AddKnowledgeGroup("B", "", "", "")
	s.SetKnowledgeGroupSources(a.ID, []string{"shared.md", "only-a.md"})
	s.SetKnowledgeGroupSources(b.ID, []string{"shared.md"})

	m, err := s.GroupsForSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(m["shared.md"]) != 2 {
		t.Errorf("shared.md groups = %v, want both", m["shared.md"])
	}
	if len(m["only-a.md"]) != 1 || m["only-a.md"][0] != a.ID {
		t.Errorf("only-a.md groups = %v", m["only-a.md"])
	}
}

// A project belongs to exactly one organization, so moving replaces.
func TestMoveProjectReplacesOrg(t *testing.T) {
	s := openKB(t)
	o1, _ := s.AddKnowledgeOrg("Acme")
	o2, _ := s.AddKnowledgeOrg("Beta")
	p, _ := s.AddKnowledgeProject("決済基盤", o1.ID)
	if ok, err := s.MoveKnowledgeProject(p.ID, o2.ID); err != nil || !ok {
		t.Fatalf("move: %v %v", ok, err)
	}
	ps, _ := s.KnowledgeProjects()
	if len(ps) != 1 || ps[0].OrgID != o2.ID {
		t.Errorf("projects = %+v, want org %s", ps, o2.ID)
	}
}

// An empty graph must read as empty lists, not nil: nil marshals to JSON null
// and the screen maps over these on first render.
func TestEmptyGraphReadsAsEmptyLists(t *testing.T) {
	s := openKB(t)
	orgs, _ := s.KnowledgeOrgs()
	projects, _ := s.KnowledgeProjects()
	groups, _ := s.KnowledgeGroups()
	rels, _ := s.KnowledgeRelations()
	if orgs == nil || projects == nil || groups == nil || rels == nil {
		t.Error("an empty graph must yield empty slices, never nil")
	}
}

// A group with no links must still present empty slices for the same reason.
func TestGroupWithoutLinksHasEmptySlices(t *testing.T) {
	s := openKB(t)
	s.AddKnowledgeGroup("A", "", "", "")
	gs, _ := s.KnowledgeGroups()
	if gs[0].Projects == nil || gs[0].Sources == nil {
		t.Error("unlinked group must have empty, non-nil slices")
	}
}

func TestSlug(t *testing.T) {
	for _, c := range []struct{ in, prefix, want string }{
		{"Payments API", "grp", "grp-payments-api"},
		{"  Q3 Release  ", "grp", "grp-q3-release"},
		{"a//b", "", "a-b"},
	} {
		if got := Slug(c.in, c.prefix); got != c.want {
			t.Errorf("Slug(%q,%q) = %q, want %q", c.in, c.prefix, got, c.want)
		}
	}
	// Non-latin names must still yield something usable and deterministic.
	if a, b := Slug("認証", "grp"), Slug("認証", "grp"); a != b {
		t.Errorf("Slug is not deterministic: %q vs %q", a, b)
	}
}
