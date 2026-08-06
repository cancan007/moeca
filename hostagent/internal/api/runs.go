package api

import (
	"net/http"
	"strconv"

	"orchestra/hostagent/internal/store"
)

// handleRunRecord persists a manually-launched agent run (from Delivery) so it
// joins the run history and the optimization loop.
func (s *Server) handleRunRecord(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Repo        string `json:"repo"`
		Branch      string `json:"branch"`
		Task        string `json:"task"`
		Template    string `json:"template"`
		TemplateRef string `json:"templateRef"`
		ContainerID string `json:"containerId"`
		RunID       string `json:"runId"`
	}
	if !decode(w, r, &req) {
		return
	}
	id, err := s.store.RecordAgentRun(store.AgentRun{
		Source: "manual", Name: req.Name, Repo: req.Repo, Branch: req.Branch, Task: req.Task,
		Template: req.Template, TemplateRef: req.TemplateRef, ContainerID: req.ContainerID, RunID: req.RunID,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

// handleRunList returns recent manual agent runs.
func (s *Server) handleRunList(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	runs, err := s.store.AgentRuns(limit)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if runs == nil {
		runs = []store.AgentRun{}
	}
	writeJSON(w, 200, map[string]any{"runs": runs})
}
