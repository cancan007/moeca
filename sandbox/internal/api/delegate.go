package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// This file implements the host side of runtime supervisor delegation. A stage
// agent asks for a sub-agent by writing .orchestra/delegate/<id>/request.json
// into its worktree and blocking on result.json. The controller (host-side,
// where docker lives) watches that directory, runs the requested sub-agent as a
// hardened container in the SAME worktree, and writes result.json back. The
// agent opens NO network path to the host — the whole exchange is files in the
// worktree — so delegation preserves the sandbox→host isolation boundary.

const (
	delegateSubdir = ".orchestra/delegate"
	// subagentResultFile is where a sub-agent is asked to leave its summary.
	subagentResultFile = ".orchestra/subagent-result.md"
	delegatePollEvery  = 500 * time.Millisecond
)

// watchDelegations polls a running stage's worktree for delegation requests and
// fulfills them one at a time until the stage container exits (done closed).
// The policy is the parent stage's already-pinned image: a sub-agent runs the
// same bytes as the stage that spawned it, never a freshly-resolved tag.
func (s *Server) watchDelegations(worktreeDir string, run *Run, stage Stage, policy ImagePolicy, strict bool, done <-chan struct{}) {
	base := filepath.Join(worktreeDir, delegateSubdir)
	ticker := time.NewTicker(delegatePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.processPending(base, worktreeDir, run, stage, policy, strict)
		}
	}
}

// delegateRequest is the sub-agent request written by the spawn_subagent tool.
type delegateRequest struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Task  string `json:"task"`
	Model string `json:"model"`
}

// processPending fulfills every request that has no result yet. It runs
// synchronously (in the watcher goroutine), so sub-agents run one at a time —
// matching the caller, which blocks on each spawn_subagent call in turn.
func (s *Server) processPending(base, worktreeDir string, run *Run, stage Stage, policy ImagePolicy, strict bool) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return // no delegate dir yet
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "result.json")); err == nil {
			continue // already fulfilled
		}
		raw, err := os.ReadFile(filepath.Join(dir, "request.json"))
		if err != nil {
			continue // request not fully written yet (rename pending)
		}
		var req delegateRequest
		if json.Unmarshal(raw, &req) != nil {
			s.writeDelegateResult(dir, map[string]any{"error": "invalid request", "exitCode": -1})
			continue
		}
		s.runChild(dir, worktreeDir, run, stage, policy, strict, req)
	}
}

// runChild launches one sub-agent container in the stage's worktree, waits for
// it, and writes result.json (the sub-agent's summary + exit code, or an error).
func (s *Server) runChild(dir, worktreeDir string, run *Run, stage Stage, policy ImagePolicy, strict bool, req delegateRequest) {
	model := req.Model
	if model == "" {
		model = stage.Model
	}
	env := map[string]string{
		"ORCHESTRA_SYSTEM": req.Role + "\n\nWhen you finish, write a concise summary of what you changed and why to " + subagentResultFile + ".",
		"ORCHESTRA_TASK":   req.Task,
		"ORCHESTRA_RUN":    run.ID,
		"ORCHESTRA_STAGE":  stage.ID + "-sub-" + req.ID,
		// The child is one level deeper and (at maxDepth) cannot itself delegate.
		"ORCHESTRA_DELEGATE_DEPTH": strconv.Itoa(1),
		"ORCHESTRA_DELEGATE_MAX":   strconv.Itoa(run.maxDepth),
	}
	if model != "" {
		env["ORCHESTRA_MODEL"] = model
	}
	// Inherit the parent stage's provider route so the child speaks the same
	// dialect through the same gateway origin.
	if stage.Provider != "" {
		env["ORCHESTRA_PROVIDER"] = stage.Provider
	}
	if stage.ProviderPrefix != "" {
		env["ORCHESTRA_BASE_URL"] = strings.TrimRight(s.cfg.gatewayStrictBase(), "/") + "/" + strings.Trim(stage.ProviderPrefix, "/")
	}
	// Inherit the parent stage's cost controls so a sub-agent doesn't silently
	// run at the (more expensive) agent defaults.
	if stage.Effort != "" {
		env["ORCHESTRA_EFFORT"] = stage.Effort
	}
	if stage.MaxTokens > 0 {
		env["ORCHESTRA_MAX_TOKENS"] = strconv.Itoa(stage.MaxTokens)
	}

	taskID := sanitizeID(run.TaskID + "-" + stage.ID + "-sub-" + req.ID)
	// A sub-agent inherits its parent run's session, and therefore its
	// knowledge scope: delegation must not be a way to widen what can be read.
	spec := s.buildSpec(taskID, worktreeDir, policy, nil, env, strict, s.sessionFor(run, stage.ID))
	cid, err := s.docker.Create(spec)
	if err != nil {
		s.writeDelegateResult(dir, map[string]any{"error": err.Error(), "exitCode": -1})
		return
	}
	code, werr := s.docker.Wait(cid)
	_ = s.docker.Remove(cid)
	if werr != nil {
		s.writeDelegateResult(dir, map[string]any{"error": werr.Error(), "exitCode": -1})
		return
	}
	s.writeDelegateResult(dir, map[string]any{
		"result":   readSubagentResult(worktreeDir),
		"exitCode": code,
	})
}

// writeDelegateResult atomically publishes result.json so the caller (polling
// for it) never reads a partial file.
func (s *Server) writeDelegateResult(dir string, v map[string]any) {
	b, _ := json.Marshal(v)
	tmp := filepath.Join(dir, "result.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, filepath.Join(dir, "result.json"))
	}
}

// readSubagentResult consumes the sub-agent's summary file (so the next
// delegation starts clean) and returns its contents, if any.
func readSubagentResult(worktreeDir string) string {
	p := filepath.Join(worktreeDir, subagentResultFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	os.Remove(p)
	return strings.TrimSpace(string(b))
}
