package api

import (
	"log"
	"net/http"

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
//
// What this function returns is the whole of it. Nothing downstream adds a
// group: see the note on relations below.

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

// Relations do not widen a scope.
//
// They did, briefly, bounded by a hop count an agent template carried. The bound
// made it safe in the sense that it could not run away, but not in the sense
// that mattered: the scope on a task states which groups its agents may reach,
// and a second setting on a different screen — owned by whoever wrote the agent,
// not by whoever set the scope — could enlarge it. A boundary that another
// decision can move is not a boundary, and the task said "Kon_Tube" while
// granting whatever the graph happened to connect.
//
// Measured on a real graph, one hop took a project's scope from 7 groups to 10
// of the 11 that existed. That is the feature working as designed, which is the
// argument against it.
//
// So a relation is documentation again, which is what it was before scopes
// existed: it says how two bodies of knowledge stand to each other, it is drawn
// and read by people, and it grants nothing. What a scope resolves to is final.

// applyStageScopes writes the run's group set onto every stage.
//
// Every stage of a run gets the same reach, because the scope is the whole of
// what a run may retrieve and nothing below it narrows or widens. The value is
// still written per stage rather than left to be inherited: a stage carrying no
// groups key falls back to the run's session, and "the same as the run" is worth
// saying rather than inferring — the controller mints sessions from what it is
// told, and silence there would be indistinguishable from an unscoped stage.
func applyStageScopes(spec map[string]any, groups []string) {
	stages, ok := spec["stages"].([]any)
	if !ok {
		return
	}
	for _, raw := range stages {
		st, ok := raw.(map[string]any)
		if !ok {
			continue
		}
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
	writeJSON(w, 200, map[string]any{"groups": groups, "scoped": scoped})
}
