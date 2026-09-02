package api

import (
	"net/http"
	"strings"

	"orchestra/hostagent/internal/store"
)

// The Knowledge graph's host-side API.
//
// The host owns this data: it is authored in the UI, must survive a restart,
// and must be editable while the indexer container is down. The indexer is fed
// from it rather than the other way round — same arrangement as knowledge
// sources themselves.
//
// Group ids are the permission tags the gateway hands to the indexer, so they
// are generated once at creation and never edited. Renaming a group changes
// only its label.

// knowledgeGraph is the whole graph in one response. The screen needs all of it
// on load — the rail lists groups, the canvas needs relations, the assignment
// view needs orgs and projects — so it is one round trip rather than four.
type knowledgeGraph struct {
	Orgs      []store.KnowledgeOrg      `json:"orgs"`
	Projects  []store.KnowledgeProject  `json:"projects"`
	Groups    []store.KnowledgeGroup    `json:"groups"`
	Relations []store.KnowledgeRelation `json:"relations"`
}

// storeReady answers with a 503 when persistence is unavailable, so the screen
// can say the graph cannot be loaded instead of rendering as if it were empty.
func (s *Server) storeReady(w http.ResponseWriter) bool {
	if s.store == nil {
		writeErr(w, 503, "knowledge storage is unavailable")
		return false
	}
	return true
}

func (s *Server) handleKnowledge(w http.ResponseWriter, _ *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var g knowledgeGraph
	var err error
	if g.Orgs, err = s.store.KnowledgeOrgs(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if g.Projects, err = s.store.KnowledgeProjects(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if g.Groups, err = s.store.KnowledgeGroups(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if g.Relations, err = s.store.KnowledgeRelations(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, g)
}

// --- organizations ---

func (s *Server) handleKnowledgeOrg(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct{ ID, Name string }
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	// An id in the body means "rename this one"; its absence means "create".
	if req.ID != "" {
		ok, err := s.store.RenameKnowledgeOrg(req.ID, req.Name)
		s.writeUpdated(w, ok, err, "organization")
		return
	}
	org, err := s.store.AddKnowledgeOrg(req.Name)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, org)
}

func (s *Server) handleKnowledgeOrgDelete(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	ok, err := s.store.DeleteKnowledgeOrg(r.URL.Query().Get("id"))
	s.writeUpdated(w, ok, err, "organization")
}

// --- projects ---

func (s *Server) handleKnowledgeProject(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct{ ID, Name, OrgID string }
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.ID != "" {
		// Name and organization are edited independently, so an empty field
		// means "leave it alone" rather than "clear it".
		if req.Name != "" {
			if ok, err := s.store.RenameKnowledgeProject(req.ID, req.Name); err != nil || !ok {
				s.writeUpdated(w, ok, err, "project")
				return
			}
		}
		if req.OrgID != "" {
			if ok, err := s.store.MoveKnowledgeProject(req.ID, req.OrgID); err != nil || !ok {
				s.writeUpdated(w, ok, err, "project")
				return
			}
		}
		writeJSON(w, 200, map[string]string{"updated": req.ID})
		return
	}
	if req.Name == "" || req.OrgID == "" {
		writeErr(w, 400, "name and orgId are required")
		return
	}
	// Without an existence check the project would be created under an
	// organization that is not there, and would never appear in the tree.
	if ok, err := s.store.KnowledgeExists("knowledge_orgs", req.OrgID); err != nil {
		writeErr(w, 500, err.Error())
		return
	} else if !ok {
		writeErr(w, 404, "unknown organization: "+req.OrgID)
		return
	}
	p, err := s.store.AddKnowledgeProject(req.Name, req.OrgID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, p)
}

func (s *Server) handleKnowledgeProjectDelete(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	ok, err := s.store.DeleteKnowledgeProject(r.URL.Query().Get("id"))
	s.writeUpdated(w, ok, err, "project")
}

// --- groups ---

func (s *Server) handleKnowledgeGroup(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct{ ID, Name, Color, Owner, Description string }
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	if req.ID != "" {
		ok, err := s.store.UpdateKnowledgeGroup(req.ID, req.Name, req.Color, req.Owner, req.Description)
		s.writeUpdated(w, ok, err, "group")
		return
	}
	g, err := s.store.AddKnowledgeGroup(req.Name, req.Color, req.Owner, req.Description)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, g)
}

func (s *Server) handleKnowledgeGroupDelete(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	ok, err := s.store.DeleteKnowledgeGroup(r.URL.Query().Get("id"))
	// Deleting a group revokes every source it granted, which is the direction
	// worth being prompt about: the indexer should stop honouring it now, not
	// at the next run.
	if ok && err == nil {
		s.syncKnowledgeGroupsLogged("group deleted")
	}
	s.writeUpdated(w, ok, err, "group")
}

// handleKnowledgeGroupLinks replaces a group's project links or its source
// membership. Both are edited as a set in the UI, so both are submitted whole:
// sending a partial list would quietly revoke whatever was omitted, which for
// sources means revoking retrieval access.
func (s *Server) handleKnowledgeGroupLinks(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct {
		GroupID  string   `json:"groupId"`
		Projects []string `json:"projects"`
		Sources  []string `json:"sources"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.GroupID == "" {
		writeErr(w, 400, "groupId is required")
		return
	}
	if ok, err := s.store.KnowledgeExists("knowledge_groups", req.GroupID); err != nil {
		writeErr(w, 500, err.Error())
		return
	} else if !ok {
		writeErr(w, 404, "unknown group: "+req.GroupID)
		return
	}
	// nil means "not submitted"; an empty slice means "clear it". Treating the
	// two alike would wipe a group's sources every time only its projects were
	// edited.
	if req.Projects != nil {
		if err := s.store.SetKnowledgeGroupProjects(req.GroupID, req.Projects); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if req.Sources != nil {
		if err := s.store.SetKnowledgeGroupSources(req.GroupID, req.Sources); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		// This is the edit that changes what the indexer may return, so it is
		// the one that is pushed. Project links do not: they decide which groups
		// a scope resolves to, which is answered here at launch, not there.
		s.syncKnowledgeGroupsLogged("group sources changed")
	}
	writeJSON(w, 200, map[string]string{"updated": req.GroupID})
}

// --- relations ---

func (s *Server) handleKnowledgeRelation(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct{ ID, From, To, Type string }
	if !decode(w, r, &req) {
		return
	}
	if req.Type == "" || !RelationTypes[req.Type] {
		writeErr(w, 400, "unknown relation type: "+req.Type)
		return
	}
	if req.ID != "" {
		ok, err := s.store.SetKnowledgeRelationType(req.ID, req.Type)
		s.writeUpdated(w, ok, err, "relation")
		return
	}
	if req.From == "" || req.To == "" {
		writeErr(w, 400, "from and to are required")
		return
	}
	// A relation to itself draws nothing and expands to what the caller already
	// has, so it is a mistake rather than a degenerate case worth storing.
	if req.From == req.To {
		writeErr(w, 400, "a relation needs two distinct groups")
		return
	}
	for _, id := range []string{req.From, req.To} {
		if ok, err := s.store.KnowledgeExists("knowledge_groups", id); err != nil {
			writeErr(w, 500, err.Error())
			return
		} else if !ok {
			writeErr(w, 404, "unknown group: "+id)
			return
		}
	}
	rel, err := s.store.AddKnowledgeRelation(req.From, req.To, req.Type)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, rel)
}

func (s *Server) handleKnowledgeRelationDelete(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	ok, err := s.store.DeleteKnowledgeRelation(r.URL.Query().Get("id"))
	s.writeUpdated(w, ok, err, "relation")
}

// writeUpdated turns a (found, err) pair into the one response shape these
// handlers share.
func (s *Server) writeUpdated(w http.ResponseWriter, ok bool, err error, what string) {
	switch {
	case err != nil:
		writeErr(w, 500, err.Error())
	case !ok:
		writeErr(w, 404, "unknown "+what)
	default:
		writeJSON(w, 200, map[string]string{"ok": what})
	}
}
