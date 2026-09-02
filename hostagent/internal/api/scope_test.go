package api

import (
	"testing"

	"orchestra/hostagent/internal/store"
)

// Relations were documentation — "this one requires that one" — and reading
// them as grants is only safe because of the bound. Without it, one edge drawn
// on the canvas could connect every group in the graph.

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

func TestRelationDepthBoundsTheWidening(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	rel(t, s, id["b"], id["c"], "requires")
	rel(t, s, id["c"], id["d"], "requires")

	cases := map[int]int{0: 1, 1: 2, 2: 3, 3: 4, 9: 4}
	for depth, want := range cases {
		if got := s.expandGroups([]string{id["a"]}, depth); len(got) != want {
			t.Errorf("depth %d reached %v, want %d groups", depth, got, want)
		}
	}
}

// The edge that warns must not also grant: pulling in knowledge declared to
// contradict the scope would blend both into one answer without saying so.
func TestConflictsWithIsNeverFollowed(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "conflicts-with")
	got := s.expandGroups([]string{id["a"]}, 5)
	if len(got) != 1 || got[0] != id["a"] {
		t.Errorf("expand = %v, want only the seed", got)
	}
}

// Edges were authored with a direction: "A requires B" says nothing about
// reading B.
func TestExpansionFollowsTheEdgeDirection(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	if got := s.expandGroups([]string{id["b"]}, 3); len(got) != 1 {
		t.Errorf("expand from the target = %v, want only the seed", got)
	}
}

// A cycle must terminate, and must not repeat a group.
func TestExpansionTerminatesOnACycle(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	rel(t, s, id["b"], id["a"], "requires")
	got := s.expandGroups([]string{id["a"]}, 10)
	if len(got) != 2 {
		t.Errorf("expand = %v, want each group once", got)
	}
}

// Widening an empty scope must stay empty: "entitled to nothing" has no seed to
// follow relations from, and inventing one would grant what was refused.
func TestExpandingAnEmptyScopeGrantsNothing(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	if got := s.expandGroups([]string{}, 5); len(got) != 0 {
		t.Errorf("expand = %v, want none", got)
	}
}

// Each stage carries its own resolved set, including at depth 0 — a stage with
// no groups key would fall back to the run's session, so "same as the run" has
// to be stated rather than inferred.
func TestStageScopesAreStatedPerStage(t *testing.T) {
	spec := map[string]any{"stages": []any{
		map[string]any{"id": "plan"},
		map[string]any{"id": "research", "knowledgeDepth": float64(2)},
	}}
	applyStageScopes(spec, []string{"a"}, func(seed []string, depth int) []string {
		if depth == 0 {
			return seed
		}
		return append(append([]string{}, seed...), "widened")
	}, func(string, []string, int) {})
	stages := spec["stages"].([]any)
	plan := stages[0].(map[string]any)["groups"].([]string)
	research := stages[1].(map[string]any)["groups"].([]string)
	if len(plan) != 1 {
		t.Errorf("plan groups = %v, want the base scope", plan)
	}
	if len(research) != 2 {
		t.Errorf("research groups = %v, want the widened scope", research)
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
