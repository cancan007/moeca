package api

import (
	"testing"

	"orchestra/hostagent/internal/store"
)

// Relations are documentation — "this one requires that one" — and grant
// nothing. A scope states which groups a task's agents may reach, and it is the
// whole of what they may reach.

// relServer builds four groups and returns their real ids: an id is a slug of
// the name, and relations carry a foreign key onto it.
func relServer(t *testing.T) (*Server, map[string]string) {
	t.Helper()
	s := New(&Config{NoSeed: true, DataDir: t.TempDir()})
	ids := map[string]string{}
	for _, name := range []string{"a", "b", "c", "d"} {
		g, err := s.store.AddKnowledgeGroup(name, "#fff", "", "")
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = g.ID
	}
	return s, ids
}

// rel adds a relation, failing the test rather than silently doing nothing —
// the foreign key means a wrong id is a no-op, which would make every
// expansion test pass for the wrong reason.
func rel(t *testing.T, s *Server, from, to, typ string) {
	t.Helper()
	if _, err := s.store.AddKnowledgeRelation(from, to, typ); err != nil {
		t.Fatalf("relation %s -%s-> %s: %v", from, typ, to, err)
	}
}

// project makes an organization and a project to scope to, returning its id.
// The membership tables carry foreign keys, so a scope naming a project that
// does not exist is a write error rather than an empty scope.
func project(t *testing.T, s *Server) string {
	t.Helper()
	org, err := s.store.AddKnowledgeOrg("org")
	if err != nil {
		t.Fatal(err)
	}
	prj, err := s.store.AddKnowledgeProject("prj", org.ID)
	if err != nil {
		t.Fatal(err)
	}
	return prj.ID
}

// The scope is absolute: no edge, of any type, adds a group to it.
//
// This is the property the whole permission model rests on. It briefly did not
// hold — relations widened a scope, bounded by a hop count set on the agent
// template — and the bound was the wrong kind of safety. A task said "Kon_Tube"
// while a setting on another screen decided how much more than Kon_Tube it
// meant.
func TestNoRelationTypeWidensAScope(t *testing.T) {
	for _, typ := range []string{"requires", "derives-from", "same-as", "references", "supersedes", "conflicts-with"} {
		t.Run(typ, func(t *testing.T) {
			s, id := relServer(t)
			prj := project(t, s)
			rel(t, s, id["a"], id["b"], typ)
			if err := s.store.SetKnowledgeGroupProjects(id["a"], []string{prj}); err != nil {
				t.Fatal(err)
			}
			got, scoped := s.scopeGroups(&store.KnowledgeScope{Kind: "project", ID: prj})
			if !scoped {
				t.Fatal("a project scope should be scoped")
			}
			if len(got) != 1 || got[0] != id["a"] {
				t.Errorf("scope resolved to %v — a %s edge widened it", got, typ)
			}
		})
	}
}

// A chain of the strongest edge type is still no wider than the scope. Depth
// was the control that made this vary; there is no such control any more.
func TestAChainOfRequiresWidensNothing(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	rel(t, s, id["b"], id["c"], "requires")
	rel(t, s, id["c"], id["d"], "requires")
	prj := project(t, s)
	if err := s.store.SetKnowledgeGroupProjects(id["a"], []string{prj}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.scopeGroups(&store.KnowledgeScope{Kind: "project", ID: prj})
	if len(got) != 1 {
		t.Errorf("scope resolved to %v, want only the group the project serves", got)
	}
}

// What a scope DOES grant is every group serving the named project — which is
// the mechanism for widening: put the group in the project, deliberately, where
// the person who owns the scope can see it.
func TestAScopeGrantsEveryGroupServingTheProject(t *testing.T) {
	s, id := relServer(t)
	prj := project(t, s)
	for _, n := range []string{"a", "b"} {
		if err := s.store.SetKnowledgeGroupProjects(id[n], []string{prj}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.scopeGroups(&store.KnowledgeScope{Kind: "project", ID: prj})
	if len(got) != 2 {
		t.Errorf("scope resolved to %v, want both groups serving the project", got)
	}
}

// Every stage gets the same reach, and gets it stated rather than inherited: a
// stage carrying no groups key falls back to the run's session, and the
// controller cannot tell that apart from an unscoped stage.
func TestEveryStageIsGivenTheRunsScope(t *testing.T) {
	spec := map[string]any{"stages": []any{
		map[string]any{"id": "plan"},
		map[string]any{"id": "research"},
	}}
	applyStageScopes(spec, []string{"a", "b"})
	for _, raw := range spec["stages"].([]any) {
		st := raw.(map[string]any)
		got, ok := st["groups"].([]string)
		if !ok || len(got) != 2 {
			t.Errorf("stage %v groups = %v", st["id"], st["groups"])
		}
	}
}

// A scope granting nothing is still stated, on every stage. Empty is the global
// scope — entitled to the knowledge declared as everyone's — and a stage with no
// key at all would be read as having asked for no policy.
func TestAnEmptyScopeIsStillStated(t *testing.T) {
	spec := map[string]any{"stages": []any{map[string]any{"id": "plan"}}}
	applyStageScopes(spec, []string{})
	st := spec["stages"].([]any)[0].(map[string]any)
	got, ok := st["groups"].([]string)
	if !ok || got == nil || len(got) != 0 {
		t.Errorf("groups = %#v, want a stated empty set", st["groups"])
	}
}

// The task-side scope persists like the schedule-side one.
func TestTaskMetaScopePersists(t *testing.T) {
	s := New(&Config{NoSeed: true, DataDir: t.TempDir()})
	want := store.TaskMeta{Goal: "", Template: "solo:a", Scope: &store.KnowledgeScope{Kind: "organization", ID: "org-1"}}
	if err := s.store.SetTaskMeta("repo", "branch", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.store.GetTaskMeta("repo", "branch")
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope == nil || got.Scope.Kind != "organization" || got.Scope.ID != "org-1" {
		t.Errorf("scope after reload = %+v", got.Scope)
	}
}
