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
//	(unset)       → nil. No policy at all: the run searches everything, which
//	                is what every schedule did before scopes existed.
//
// Nil and empty are different answers and stay different all the way to the
// gateway: nil omits the header, empty sends it empty. Collapsing them would
// turn "entitled to nothing" into "entitled to everything".

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
// `conflicts-with` is never followed. Its whole meaning is that the two sides
// disagree, so pulling one in while answering from the other would blend
// contradictory sources into a single answer without saying so. An edge that
// warns must not also grant.
const relationConflicts = "conflicts-with"

// expandGroups returns seed plus every group reachable from it within depth
// hops. depth <= 0 returns seed unchanged.
func (s *Server) expandGroups(seed []string, depth int) []string {
	if depth <= 0 || len(seed) == 0 {
		return seed
	}
	rels, err := s.store.KnowledgeRelations()
	if err != nil {
		log.Printf("hostagent: reading knowledge relations: %v", err)
		return seed
	}
	out := make(map[string][]string, len(rels))
	for _, r := range rels {
		if r.Type == relationConflicts {
			continue
		}
		out[r.From] = append(out[r.From], r.To)
	}

	seen := map[string]bool{}
	result := make([]string, 0, len(seed))
	frontier := make([]string, 0, len(seed))
	for _, g := range seed {
		if !seen[g] {
			seen[g] = true
			result = append(result, g)
			frontier = append(frontier, g)
		}
	}
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		next := []string{}
		for _, g := range frontier {
			for _, to := range out[g] {
				if seen[to] {
					continue
				}
				seen[to] = true
				result = append(result, to)
				next = append(next, to)
			}
		}
		frontier = next
	}
	return result
}

// applyStageScopes writes each stage's own group set into the run spec.
//
// The base scope belongs to the schedule; the number of relation hops belongs
// to the agent template a stage was compiled from. A run whose planner may
// follow no relations and whose researcher may follow two is the ordinary case,
// so the group set is decided per stage and the controller gives each stage the
// session that matches.
func applyStageScopes(spec map[string]any, base []string, expand func([]string, int) []string) {
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
	groups, scoped := s.scopeGroups(&store.KnowledgeScope{Kind: kind, ID: q.Get("id")})
	depth := 0
	if d, err := strconv.Atoi(q.Get("depth")); err == nil {
		depth = d
	}
	writeJSON(w, 200, map[string]any{"groups": s.expandGroups(groups, depth), "scoped": scoped})
}
