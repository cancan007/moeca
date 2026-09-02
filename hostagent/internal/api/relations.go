package api

// What each kind of relation means when a scope is widened along it.
//
// The types used to differ only in colour and dash pattern; every one of them
// granted alike, and the single exception — conflicts-with — was a constant
// compared inline inside the traversal. That made "does this edge grant" an
// exception rather than a property, which is the shape a rule takes just before
// a second exception is added beside it and the two disagree.
//
// So each type states its own behaviour and the traversal reads the table.
// Three properties, and no cost: making one edge worth more hops than another
// would turn the agent template's "relation hops" from a count into a budget,
// and a control whose number means a different distance per path is one nobody
// can predict. Strength is expressed by whether an edge is followed at all and
// by whether it may be followed onwards.
//
// The defaults are deliberately mean. A relation is documentation first — it was
// only ever that until scopes existed — so an edge grants nothing unless someone
// decided it should, and the type people draw without thinking is the one that
// grants least.
type relationPolicy struct {
	// Traverse: may a scope widen along this edge at all.
	Traverse bool
	// Symmetric: may it be followed against its authored direction. Almost
	// nothing is: "A requires B" says reading A means needing B, and says
	// nothing whatever about reading B.
	Symmetric bool
	// Transitive: may the group reached through this edge be expanded from in
	// turn. A non-transitive edge is followed only from the groups the scope
	// started with, so it widens by one step and cannot chain.
	Transitive bool
}

// relationPolicies is the whole rule set. A type absent from this map is not a
// relation this service will store — see RelationTypes, which is derived from it
// so the two cannot drift.
var relationPolicies = map[string]relationPolicy{
	// "A requires B": reading A means needing B. The strongest edge, and the one
	// that has to chain — a runbook requiring a spec that requires a glossary is
	// three documents and one need.
	"requires": {Traverse: true, Symmetric: false, Transitive: true},

	// "A derives from B": A was produced from B, so B is the provenance of what A
	// says. Chains for the same reason requires does; provenance is a path.
	"derives-from": {Traverse: true, Symmetric: false, Transitive: true},

	// "A is the same as B": two names for one body of knowledge. The only
	// symmetric edge — identity does not have a direction. It chains, because
	// sameness is transitive by definition.
	"same-as": {Traverse: true, Symmetric: true, Transitive: true},

	// "A references B": A mentions B. This is the type the canvas creates for
	// every new edge, so it is the one drawn without deciding anything — and it
	// used to grant exactly as much as requires. It still widens by one step,
	// because "related" usually is worth a look, but it cannot chain: following
	// mentions of mentions is how one edge quietly connects a whole graph.
	"references": {Traverse: true, Symmetric: false, Transitive: false},

	// "A supersedes B": A replaces B, so B is the outdated one. Not followed.
	//
	// The direction alone made this wrong: holding the current document granted
	// the obsolete one, never the reverse. And the blend is worse-shaped than the
	// one conflicts-with prevents — two versions of the same document usually
	// agree in wording and differ in a number, so nothing in the retrieved text
	// signals that one of them is stale.
	//
	// This is not a claim that the superseded version is worthless. It is that a
	// grant is the wrong mechanism for it: reaching it usefully needs the result
	// to say "this was replaced by A", and retrieval carries no such annotation.
	"supersedes": {Traverse: false},

	// "A conflicts with B": the two disagree. Pulling one in while answering from
	// the other would blend contradictory sources into a single answer without
	// saying so. An edge that warns must not also grant.
	"conflicts-with": {Traverse: false},
}

// RelationTypes are the link kinds the graph can draw. An unknown type is
// rejected rather than stored, because the renderer has no colour or dash
// pattern for it and would drop the edge silently.
//
// Derived from the policy table so a type can never exist without a rule for
// what it grants — which is how conflicts-with came to be handled beside the
// table rather than in it.
var RelationTypes = func() map[string]bool {
	out := make(map[string]bool, len(relationPolicies))
	for t := range relationPolicies {
		out[t] = true
	}
	return out
}()

// policyOf returns the rule for a type. An unrecognised type grants nothing:
// this service refuses to store one, so reaching here means the row predates the
// type or was written by hand, and neither is a reason to widen a scope.
func policyOf(t string) relationPolicy {
	return relationPolicies[t]
}
