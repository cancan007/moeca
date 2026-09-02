package api

// The kinds of link the knowledge graph can draw.
//
// These are documentation. A relation says how two bodies of knowledge stand to
// each other — one needs another, one replaced another, two disagree — and it is
// drawn and read by people.
//
// It briefly granted as well: a scope widened along relations, bounded by a hop
// count carried on the agent template. The bound kept it from running away but
// not from being wrong. A scope states which groups a task's agents may reach,
// and a second setting on a different screen, owned by whoever wrote the agent
// rather than whoever set the scope, could enlarge it. On a real graph one hop
// took a project from 7 groups to 10 of the 11 that existed.
//
// So the traversal is gone and this is a vocabulary again. Widening a scope is
// done where it can be seen: put the group in the project.

// RelationTypes are the link kinds the graph can draw. An unknown type is
// rejected rather than stored, because the renderer has no colour or dash
// pattern for it and would drop the edge silently.
var RelationTypes = map[string]bool{
	// A needs B to be understood.
	"requires": true,
	// A was produced from B; B is the provenance of what A says.
	"derives-from": true,
	// Two names for one body of knowledge.
	"same-as": true,
	// A mentions B. The type every new edge is created as, and the weakest
	// claim in the vocabulary — which is why it is the default.
	"references": true,
	// A replaces B; B is the outdated one.
	"supersedes": true,
	// The two disagree, and an answer drawn from both would blend them.
	"conflicts-with": true,
}
