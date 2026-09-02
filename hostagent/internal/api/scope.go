package api

import (
	"log"
	"net/http"
	"strconv"

	"orchestra/hostagent/internal/store"
)

// Resolving a schedule's knowledge scope to the groups a run may retrieve.
//
// A schedule names a NODE of the Knowledge graph — global, an organization, or
// a project — rather than a list of groups. The two are not interchangeable:
// groups are added to a project over time, and a schedule that meant "this
// project's knowledge" should follow the project rather than freeze the group
// list that existed the day it was saved. So the node is stored and the groups
// are computed at launch.
//
// The tiers map onto the retrieval filter that already exists:
//
//	global        → no groups. Globally-scoped sources are exempt from the
//	                filter by design, so a caller entitled to no group sees
//	                exactly the knowledge declared as everyone's.
//	organization  → the groups serving any project of that organization.
//	project       → the groups serving that project.
//	(unset)       → nil. No entitlement was stated, and the gateway refuses
//	                retrieval outright: a schedule that never chose a scope
//	                reads no knowledge. It is the default a task is created
//	                with, so it is the one that must not be the widest grant.
//
// Nil and empty are different answers and stay different all the way to the
// gateway: nil is refused there, empty sends the header empty and retrieves the
// knowledge declared as everyone's. Collapsing them would turn "nobody chose"
// into "entitled to everything", which is the one default worth being careful
// about — it is the state every task starts in.

// scopeGroups returns the groups a scope grants. The bool reports whether a
// policy applies at all; false means an unscoped run.
func (s *Server) scopeGroups(scope *store.KnowledgeScope) ([]string, bool) {
	if scope == nil || scope.Kind == "" {
		return nil, false
	}
	switch scope.Kind {
	case "global":
		// Deliberately an empty, non-nil slice.
		return []string{}, true
	case "project":
		return s.groupsForProjects(map[string]bool{scope.ID: true}), true
	case "organization":
		projects, err := s.store.KnowledgeProjects()
		if err != nil {
			log.Printf("hostagent: resolving organization scope %s: %v", scope.ID, err)
			// An unresolvable scope must not widen into "search everything".
			return []string{}, true
		}
		want := map[string]bool{}
		for _, p := range projects {
			if p.OrgID == scope.ID {
				want[p.ID] = true
			}
		}
		return s.groupsForProjects(want), true
	default:
		log.Printf("hostagent: unknown knowledge scope kind %q; granting nothing", scope.Kind)
		return []string{}, true
	}
}

// groupsForProjects lists the groups serving any of the given projects.
func (s *Server) groupsForProjects(projects map[string]bool) []string {
	groups, err := s.store.KnowledgeGroups()
	if err != nil {
		log.Printf("hostagent: reading knowledge groups: %v", err)
		return []string{}
	}
	out := []string{}
	for _, g := range groups {
		for _, p := range g.Projects {
			if projects[p] {
				out = append(out, g.ID)
				break
			}
		}
	}
	return out
}

// Widening a scope along declared relations.
//
// A relation between two groups was documentation: "this one requires that
// one", drawn on the Knowledge canvas and read by people. Treating it as a
// grant is a deliberate change of meaning, and the reason it is safe is the
// bound — an agent template says how many hops it may follow, and zero (the
// default) keeps the old behaviour exactly. Without a bound, one edge added on
// the canvas could quietly connect every group in the graph.
//
// Traversal is DIRECTED, because that is how the edges were authored: "A
// requires B" says reading A means also needing B, and says nothing about
// reading B.
//
// Which edges grant, and how far, is stated per type in relations.go rather
// than as exceptions here. This function only walks what that table permits.

// edge is one followable step, already resolved for direction.
type edge struct {
	to     string
	typ    string
	policy relationPolicy
}

// grant records why one group ended up in a scope: because it was named by the
// scope itself, or because an edge of some type led to it from another group.
//
// Kept because a scope that widens is a scope somebody will later ask about, and
// "why was this group granted" cannot be answered from the resulting list. The
// graph it was derived from will have moved on by the time anyone asks.
type grant struct {
	Group string
	// Via is empty for a group the scope named directly.
	Via  string // relation type
	From string // the group the edge was followed from
}

// expandGroups returns seed plus every group reachable from it within depth
// hops. depth <= 0 returns seed unchanged.
func (s *Server) expandGroups(seed []string, depth int) []string {
	out := make([]string, 0, len(seed))
	for _, g := range s.expandGroupsExplained(seed, depth) {
		out = append(out, g.Group)
	}
	return out
}

// logGrants records how a scope was arrived at, once, at the moment it is
// applied to a run.
//
// The resulting group list cannot answer "why was this one included": a group
// reached through two hops of `requires` looks exactly like one the project
// named directly. Reconstructing it later means walking the graph as it is then,
// which is not the graph the run was launched against — an edge drawn since
// would make a past run look wrong, and an edge deleted since would make one
// look inexplicable.
func logGrants(what string, grants []grant) {
	for _, g := range grants {
		if g.Via == "" {
			continue // named by the scope; nothing to explain
		}
		log.Printf("hostagent: %s: group %s granted via %s from %s", what, g.Group, g.Via, g.From)
	}
}

// expandGroupsExplained is the same walk, keeping the derivation of each group.
func (s *Server) expandGroupsExplained(seed []string, depth int) []grant {
	grants := make([]grant, 0, len(seed))
	seen := map[string]bool{}
	frontier := make([]string, 0, len(seed))
	for _, g := range seed {
		if seen[g] {
			continue
		}
		seen[g] = true
		grants = append(grants, grant{Group: g})
		frontier = append(frontier, g)
	}
	if depth <= 0 || len(seed) == 0 {
		return grants
	}

	rels, err := s.store.KnowledgeRelations()
	if err != nil {
		// The scope itself still stands. Widening is an addition, and failing to
		// read the additions must not lose what was already granted.
		log.Printf("hostagent: reading knowledge relations: %v", err)
		return grants
	}
	out := map[string][]edge{}
	for _, r := range rels {
		p := policyOf(r.Type)
		if !p.Traverse {
			continue
		}
		out[r.From] = append(out[r.From], edge{to: r.To, typ: r.Type, policy: p})
		if p.Symmetric {
			out[r.To] = append(out[r.To], edge{to: r.From, typ: r.Type, policy: p})
		}
	}

	// Whether a group may be expanded FROM, which is not the same as whether it
	// is granted. A group reached through a non-transitive edge is in the scope
	// and is a dead end: "A references B" widens by one step, and following B's
	// own mentions from there is how one edge connects a whole graph.
	canExpand := map[string]bool{}
	for _, g := range frontier {
		canExpand[g] = true
	}

	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		next := []string{}
		for _, g := range frontier {
			if !canExpand[g] {
				continue
			}
			for _, e := range out[g] {
				if seen[e.to] {
					continue
				}
				seen[e.to] = true
				grants = append(grants, grant{Group: e.to, Via: e.typ, From: g})
				canExpand[e.to] = e.policy.Transitive
				next = append(next, e.to)
			}
		}
		frontier = next
	}
	return grants
}

// applyStageScopes writes each stage's own group set into the run spec.
//
// The base scope belongs to the schedule; the number of relation hops belongs
// to the agent template a stage was compiled from. A run whose planner may
// follow no relations and whose researcher may follow two is the ordinary case,
// so the group set is decided per stage and the controller gives each stage the
// session that matches.
func applyStageScopes(spec map[string]any, base []string, expand func([]string, int) []string, explain func(stage string, base []string, depth int)) {
	stages, ok := spec["stages"].([]any)
	if !ok {
		return
	}
	for _, raw := range stages {
		st, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		depth := 0
		if d, ok := st["knowledgeDepth"].(float64); ok {
			depth = int(d)
		}
		groups := expand(base, depth)
		if depth > 0 {
			// Per stage, because the hop bound is per stage: two stages of one run
			// can hold different scopes, and a line naming only the run would not
			// say which.
			id, _ := st["id"].(string)
			explain(id, base, depth)
		}
		// Always stated, even at depth 0 and even when it equals the run's own
		// set: a stage with no groups key would fall back to the run session,
		// and "same as the run" must be said rather than inferred.
		st["groups"] = groups
	}
}

// handleKnowledgeScope resolves a scope node (and an optional relation depth)
// to the groups it grants.
//
// It exists because Delivery starts its runs from the UI rather than through
// this service, and the resolution must not be reimplemented there: the graph
// lives here, and one implementation is the only way "project X" means the same
// thing from both screens.
func (s *Server) handleKnowledgeScope(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind == "" {
		writeJSON(w, 200, map[string]any{"groups": nil, "scoped": false})
		return
	}
	// Delivery calls this immediately before it launches, so it is this
	// service's last sight of a run that is about to search. Refresh the
	// indexer's mapping here: resolving a scope and enforcing it are two halves
	// of one answer, and they should not be able to disagree.
	s.syncKnowledgeGroupsLogged("scope resolved")
	groups, scoped := s.scopeGroups(&store.KnowledgeScope{Kind: kind, ID: q.Get("id")})
	depth := 0
	if d, err := strconv.Atoi(q.Get("depth")); err == nil {
		depth = d
	}
	writeJSON(w, 200, map[string]any{"groups": s.expandGroups(groups, depth), "scoped": scoped})
}
