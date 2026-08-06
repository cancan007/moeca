package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"orchestra/hostagent/internal/store"
	"orchestra/hostagent/internal/tasksource"
)

// handleSources lists the configured Daily task sources (Jira / Trello /
// Notion / demo).
func (s *Server) handleSources(w http.ResponseWriter, _ *http.Request) {
	all := s.currentSources().Names()
	out := make([]string, 0, len(all))
	for _, n := range all {
		if n != deliverySource { // github is a Delivery source, not a Daily one
			out = append(out, n)
		}
	}
	writeJSON(w, 200, map[string]any{"sources": out})
}

// handleSourceConfigAdd persists a task source and rebuilds the registry.
func (s *Server) handleSourceConfigAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = req.Type
	}
	switch req.Type {
	case "jira", "trello", "notion":
	default:
		writeErr(w, 400, "type must be jira, trello or notion")
		return
	}
	if err := s.store.AddTaskSource(name, req.Type); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.rebuildSources()
	writeJSON(w, 201, map[string]string{"name": name, "type": req.Type})
}

// handleSourceConfigDelete removes a task source and rebuilds the registry.
func (s *Server) handleSourceConfigDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	ok, err := s.store.RemoveTaskSource(name)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "unknown source")
		return
	}
	s.rebuildSources()
	writeJSON(w, 200, map[string]string{"removed": name})
}

// handlePull performs an explicit pull from one source: fetch through the
// gateway-routed adapter, upsert into the store, and advance the incremental
// cursor. This is the "pull型" ingest — nothing is synced automatically.
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("source")
	src, ok := s.currentSources().Get(name)
	if !ok {
		writeErr(w, 404, "unknown source")
		return
	}
	cursorKey := "pull_cursor:" + name
	cursor, _ := s.store.GetState(cursorKey)
	tickets, err := src.Fetch(r.Context(), tasksource.Query{Assignee: "@me", State: "open", Since: cursor})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if err := s.store.UpsertTickets(tickets); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, t := range tickets {
		if t.UpdatedAt > cursor {
			cursor = t.UpdatedAt
		}
	}
	if err := s.store.SetState(cursorKey, cursor); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"pulled": len(tickets), "tickets": tickets})
}

// handleRuns returns recent schedule occurrences (executed / missed) for the
// Daily run history.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	runs, err := s.store.Runs(limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if runs == nil {
		runs = []store.ScheduleRun{}
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}

// handleTickets returns stored Daily tickets. With no ?source= it excludes the
// Delivery-only github source; with an explicit source it returns just that one.
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	var ts []tasksource.Ticket
	var err error
	if source == "" {
		ts, err = s.store.TicketsExcluding(deliverySource)
	} else {
		ts, err = s.store.Tickets(source)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ts == nil {
		ts = []tasksource.Ticket{}
	}
	writeJSON(w, 200, map[string]any{"tickets": ts})
}

type promoteReq struct {
	ID   string `json:"id"`   // pulled ticket id, e.g. "jira:PROJ-42"
	Repo string `json:"repo"` // target hostagent repo name (defaults to the ticket's)
	Base string `json:"base"` // base branch (defaults to the repo target)
}

// handlePromote turns a pulled Daily ticket into a Delivery task: it creates a
// git worktree (branch "ticket/<id>") off the base branch, so the ticket can be
// worked and reviewed through the existing Delivery flow. This is the bridge
// from Daily (external SoR, pull) to Delivery (git, worktree/PR).
func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	var req promoteReq
	if !decode(w, r, &req) {
		return
	}
	ticket, err := s.store.TicketByID(req.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ticket == nil {
		writeErr(w, 404, "unknown ticket")
		return
	}
	repoName := req.Repo
	if repoName == "" {
		repoName = ticket.Repo
	}
	repo, ok := s.repo(repoName)
	if !ok {
		writeErr(w, 400, "no target repo for ticket (pass 'repo' matching a configured repo)")
		return
	}
	base := req.Base
	if base == "" {
		base = repo.Target
	}
	branch := "ticket/" + sanitize(strings.ReplaceAll(ticket.ID, ":", "-"))
	dir := filepath.Join(os.TempDir(), "orchestra-wt", repo.Name, sanitize(branch))
	if err := s.g.AddWorktree(repo.Path, dir, branch, base); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// Seed the task doc from the issue so the Delivery card opens with its detail
	// (title + body), editable as .orchestra/task.md and used as the agent's task.
	taskMD := "# " + ticket.Title + "\n\n" + ticket.Body + "\n"
	if ticket.URL != "" {
		taskMD += "\n<!-- orchestra: source=" + ticket.ID + " " + ticket.URL + " -->\n"
	}
	_ = s.g.WriteFile(dir, ".orchestra/task.md", taskMD)
	writeJSON(w, 201, map[string]string{
		"ticket": ticket.ID, "repo": repo.Name, "branch": branch, "worktreePath": dir,
	})
}
