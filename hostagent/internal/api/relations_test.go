package api

import "testing"

// What each relation type grants. The types used to differ only in colour, and
// every one of them granted alike; these fix the behaviour each now states for
// itself in relations.go.
//
// Uses the group fixture from scope_test.go: four groups named a..d.

// widened reports the names a scope of `seed` reaches at `depth`.
func widened(t *testing.T, s *Server, ids map[string]string, seed string, depth int) map[string]bool {
	t.Helper()
	byID := map[string]string{}
	for name, id := range ids {
		byID[id] = name
	}
	out := map[string]bool{}
	for _, id := range s.expandGroups([]string{ids[seed]}, depth) {
		out[byID[id]] = true
	}
	return out
}

// The strongest edge, and the one that has to chain: a runbook requiring a spec
// that requires a glossary is three documents and one need.
func TestRequiresChains(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	rel(t, s, id["b"], id["c"], "requires")

	got := widened(t, s, id, "a", 2)
	if !got["b"] || !got["c"] {
		t.Errorf("requires did not chain: %v", got)
	}
	if widened(t, s, id, "a", 1)["c"] {
		t.Error("two hops were followed at depth 1")
	}
}

// Direction is the authored one. "A requires B" says nothing about reading B.
func TestTraversalStaysDirected(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	if widened(t, s, id, "b", 3)["a"] {
		t.Error("an edge was followed against its direction")
	}
}

// This is the type the canvas creates for every new edge, so it is the one drawn
// without deciding anything. It still widens by one step, but following mentions
// of mentions is how one edge quietly connects a whole graph.
func TestReferencesWidensOnceAndDoesNotChain(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "references")
	rel(t, s, id["b"], id["c"], "references")

	got := widened(t, s, id, "a", 3)
	if !got["b"] {
		t.Error("references should still widen by one step")
	}
	if got["c"] {
		t.Errorf("references chained: %v", got)
	}
}

// A group reached through a non-transitive edge is a dead end, whatever leads
// onwards from it.
func TestNothingChainsOutOfAReference(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "references")
	rel(t, s, id["b"], id["c"], "requires")

	if widened(t, s, id, "a", 3)["c"] {
		t.Error("a requires edge was followed out of a group reached by reference")
	}
}

// Holding the current document used to grant the obsolete one, never the
// reverse — and two versions of a document usually agree in wording and differ
// in a number, so nothing in the retrieved text signals which is stale.
func TestSupersedesGrantsNothing(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "supersedes")

	if widened(t, s, id, "a", 3)["b"] {
		t.Error("supersedes granted the superseded document")
	}
	if widened(t, s, id, "b", 3)["a"] {
		t.Error("supersedes granted in reverse")
	}
}

// An edge that warns must not also grant.
func TestConflictsWithGrantsNothing(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "conflicts-with")
	if widened(t, s, id, "a", 3)["b"] {
		t.Error("conflicts-with granted")
	}
}

// Identity has no direction, and sameness is transitive by definition.
func TestSameAsIsSymmetricAndChains(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "same-as")
	rel(t, s, id["b"], id["c"], "same-as")

	if !widened(t, s, id, "a", 2)["c"] {
		t.Error("same-as did not chain")
	}
	if !widened(t, s, id, "c", 2)["a"] {
		t.Error("same-as was not followed against its authored direction")
	}
}

// Zero is the default every template that has never thought about relations
// carries, so it has to stay inert whatever the graph says.
func TestDepthZeroWidensNothing(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")
	rel(t, s, id["a"], id["c"], "same-as")

	got := widened(t, s, id, "a", 0)
	if len(got) != 1 || !got["a"] {
		t.Errorf("depth 0 widened to %v", got)
	}
}

// The derivation is kept because the resulting list cannot answer "why was this
// one included": a group two hops away looks exactly like one the scope named.
func TestTheDerivationIsRecorded(t *testing.T) {
	s, id := relServer(t)
	rel(t, s, id["a"], id["b"], "requires")

	grants := s.expandGroupsExplained([]string{id["a"]}, 1)
	if len(grants) != 2 {
		t.Fatalf("grants = %+v", grants)
	}
	if grants[0].Group != id["a"] || grants[0].Via != "" {
		t.Errorf("the seed should carry no derivation: %+v", grants[0])
	}
	if grants[1].Group != id["b"] || grants[1].Via != "requires" || grants[1].From != id["a"] {
		t.Errorf("derivation = %+v", grants[1])
	}
}

// A type with no rule grants nothing. This service refuses to store one, so a
// row carrying one predates the type or was written by hand — neither is a
// reason to widen a scope.
func TestAnUnknownTypeGrantsNothing(t *testing.T) {
	if policyOf("invented-later").Traverse {
		t.Error("an unrecognised relation type traverses")
	}
}

// The accepted types are derived from the rules, so a type cannot exist without
// one — which is how conflicts-with came to be handled beside the table instead
// of in it.
func TestEveryAcceptedTypeHasARule(t *testing.T) {
	for typ := range RelationTypes {
		if _, ok := relationPolicies[typ]; !ok {
			t.Errorf("%q is accepted but has no policy", typ)
		}
	}
	for _, want := range []string{"requires", "derives-from", "references", "same-as", "supersedes", "conflicts-with"} {
		if !RelationTypes[want] {
			t.Errorf("%q is missing from the accepted types", want)
		}
	}
}
