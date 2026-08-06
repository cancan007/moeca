package api

import (
	"net/http"
	"strings"

	"orchestra/hostagent/internal/store"
	"orchestra/hostagent/internal/tasksource"
)

// handleTaskMetaGet returns a Delivery task's goal + milestones.
func (s *Server) handleTaskMetaGet(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	branch := r.URL.Query().Get("branch")
	if repo == "" || branch == "" {
		writeErr(w, 400, "repo and branch are required")
		return
	}
	m, err := s.store.GetTaskMeta(repo, branch)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if m.Milestones == nil {
		m.Milestones = []store.Milestone{}
	}
	writeJSON(w, 200, m)
}

// handleTaskMetaSet upserts a Delivery task's goal + milestones + assigned agent
// template. Fields are pointers so a caller can update one facet without
// clobbering the others (the goal editor and the template picker are separate
// UI controls writing the same row). Goal format: a goal requires at least one
// milestone.
func (s *Server) handleTaskMetaSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo       string             `json:"repo"`
		Branch     string             `json:"branch"`
		Goal       *string            `json:"goal"`
		Milestones *[]store.Milestone `json:"milestones"`
		Template   *string            `json:"template"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Repo == "" || req.Branch == "" {
		writeErr(w, 400, "repo and branch are required")
		return
	}
	cur, err := s.store.GetTaskMeta(req.Repo, req.Branch)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if req.Goal != nil {
		cur.Goal = *req.Goal
	}
	if req.Milestones != nil {
		cur.Milestones = *req.Milestones
	}
	if req.Template != nil {
		cur.Template = *req.Template
	}
	if strings.TrimSpace(cur.Goal) != "" {
		n := 0
		for _, m := range cur.Milestones {
			if strings.TrimSpace(m.Title) != "" {
				n++
			}
		}
		if n == 0 {
			writeErr(w, 400, "a goal requires at least one milestone")
			return
		}
	}
	if err := s.store.SetTaskMeta(req.Repo, req.Branch, cur); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"repo": req.Repo, "branch": req.Branch})
}

// handleRepos lists the effective repositories so the Delivery UI can map a
// pulled GitHub issue onto a local repo (name + default base branch) and the
// Settings panel can manage them (path, ciCommand, and a `managed` flag marking
// store-backed repos that can be removed via the API — config-file seeds cannot).
func (s *Server) handleRepos(w http.ResponseWriter, _ *http.Request) {
	type repoInfo struct {
		Name      string   `json:"name"`
		Path      string   `json:"path"`
		Target    string   `json:"target"`
		CICommand []string `json:"ciCommand"`
		Managed   bool     `json:"managed"`
		// GitHubSlug ("owner/repo") from the repo's git origin, so the UI can map
		// a pulled GitHub issue onto its registered local repo automatically.
		// Empty when the repo has no github.com origin.
		GitHubSlug string `json:"githubSlug,omitempty"`
	}
	managed := map[string]bool{}
	if s.store != nil {
		if rows, err := s.store.Repos(); err == nil {
			for _, r := range rows {
				managed[r.Name] = true
			}
		}
	}
	repos := s.activeRepos()
	out := make([]repoInfo, 0, len(repos))
	for _, r := range repos {
		slug, _ := s.g.GitHubSlug(r.Path) // empty for non-github repos
		out = append(out, repoInfo{Name: r.Name, Path: r.Path, Target: r.Target, CICommand: r.CICommand, Managed: managed[r.Name], GitHubSlug: slug})
	}
	writeJSON(w, 200, map[string]any{"repos": out})
}

// handleRepoAdd upserts a store-managed Delivery repository (persisted, survives
// restart). The path is validated as a git work tree so a mistyped path fails
// fast instead of surfacing later as an empty board.
func (s *Server) handleRepoAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string   `json:"name"`
		Path      string   `json:"path"`
		Target    string   `json:"target"`
		CICommand []string `json:"ciCommand"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Path = strings.TrimSpace(req.Path)
	req.Target = strings.TrimSpace(req.Target)
	if req.Name == "" || req.Path == "" {
		writeErr(w, 400, "name and path are required")
		return
	}
	if req.Target == "" {
		req.Target = "main"
	}
	if !s.g.IsRepo(req.Path) {
		writeErr(w, 400, "not a git repository: "+req.Path)
		return
	}
	// Drop empty CI tokens (a blank input should mean "no CI", not [""]).
	ci := req.CICommand[:0]
	for _, t := range req.CICommand {
		if strings.TrimSpace(t) != "" {
			ci = append(ci, t)
		}
	}
	if err := s.store.AddRepo(req.Name, req.Path, req.Target, ci); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"added": req.Name})
}

// handleRepoDelete removes a store-managed repository. Config-file seed repos are
// not in the store and cannot be removed here.
func (s *Server) handleRepoDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	ok, err := s.store.RemoveRepo(name)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "repo not found (config-file repos cannot be removed here): "+name)
		return
	}
	writeJSON(w, 200, map[string]string{"removed": name})
}

// handleDeliveryPull pulls GitHub issues and stores them as Delivery tickets
// (source "github"). ?repo=<registered repo name> scopes to that repo's open
// issues (owner/repo derived from its git origin). Auth prefers a host-side
// GitHub App (direct api.github.com, per-repo installation token — the trusted
// host agent needs no gateway isolation); otherwise it falls back to the
// gateway-routed source (assignee-wide feed, incremental via the stored cursor).
func (s *Server) handleDeliveryPull(w http.ResponseWriter, r *http.Request) {
	cursorKey := "pull_cursor:" + deliverySource

	var slug string
	if name := strings.TrimSpace(r.URL.Query().Get("repo")); name != "" {
		repo, ok := s.repo(name)
		if !ok {
			writeErr(w, 404, "unknown repo: "+name)
			return
		}
		gs, err := s.g.GitHubSlug(repo.Path)
		if err != nil {
			writeErr(w, 400, "cannot determine GitHub repo for "+name+": "+err.Error())
			return
		}
		slug = gs
	}

	var tickets []tasksource.Ticket

	if app := s.currentGHApp(); app != nil && slug != "" {
		// Host-side GitHub App: direct, per-repo, no gateway.
		owner, rp, _ := strings.Cut(slug, "/")
		raw, err := app.Get(r.Context(), owner, rp, "/repos/"+slug+"/issues?state=open&per_page=50")
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		tickets, err = tasksource.ParseIssuesForRepo(raw, deliverySource, "", slug)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	} else {
		src, ok := s.currentSources().Get(deliverySource)
		if !ok {
			writeErr(w, 400, "no GitHub auth configured: set a host-side GitHub App in Settings, or gateway.url + a token")
			return
		}
		q := tasksource.Query{Assignee: "@me", State: "open", Repo: slug}
		if slug == "" {
			q.Since, _ = s.store.GetState(cursorKey)
		}
		var err error
		tickets, err = src.Fetch(r.Context(), q)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	}

	if err := s.store.UpsertTickets(tickets); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if slug == "" { // advance the cursor only for the assignee-wide gateway feed
		cursor, _ := s.store.GetState(cursorKey)
		for _, t := range tickets {
			if t.UpdatedAt > cursor {
				cursor = t.UpdatedAt
			}
		}
		_ = s.store.SetState(cursorKey, cursor)
	}
	writeJSON(w, 200, map[string]any{"pulled": len(tickets), "issues": tickets})
}

// handleDeliveryIssues returns the stored GitHub-sourced Delivery tickets.
func (s *Server) handleDeliveryIssues(w http.ResponseWriter, _ *http.Request) {
	ts, err := s.store.Tickets(deliverySource)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ts == nil {
		ts = []tasksource.Ticket{}
	}
	writeJSON(w, 200, map[string]any{"issues": ts})
}
