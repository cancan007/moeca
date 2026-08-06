package tools

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"orchestra/agent/internal/llm"
)

// Delegation is a file-based channel: spawn_subagent writes a request under
// /work/.orchestra/delegate/<id>/ and blocks polling for a result written by the
// host controller (which runs the sub-agent). The agent opens NO network path to
// the host — it only touches its own worktree — so runtime delegation preserves
// the sandbox→host isolation boundary.
const delegateDir = ".orchestra/delegate"

// EnableDelegation turns on the spawn_subagent tool. It is called only when the
// controller granted this agent a delegation budget (depth < max), so sub-agents
// cannot themselves delegate without bound.
func (r *Registry) EnableDelegation() {
	r.delegate = true
	if r.delegateTimeout == 0 {
		r.delegateTimeout = 30 * time.Minute
	}
	if r.delegatePoll == 0 {
		r.delegatePoll = 500 * time.Millisecond
	}
}

// spawnSubagentDef is the tool schema advertised when delegation is enabled.
func spawnSubagentDef() llm.Tool {
	return llm.Tool{
		Name:        "spawn_subagent",
		Description: "Delegate a self-contained subtask to a fresh sub-agent that works in the SAME /work worktree and reports back. Use this as a supervisor to specialize or parallelize work: the sub-agent's file changes persist in /work. Blocks until the sub-agent finishes and returns its summary.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"role": map[string]any{
					"type":        "string",
					"description": "The sub-agent's role/system instructions (who it is and how to behave).",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "The concrete task for the sub-agent to complete.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "Optional model id override for the sub-agent.",
				},
			},
			"required": []string{"role", "task"},
		},
	}
}

// spawnSubagent writes a delegation request into the worktree and blocks until
// the controller writes a result (or the timeout elapses). The sub-agent's edits
// land in /work directly, so the caller sees them on return.
func (r *Registry) spawnSubagent(role, task, model string) (string, bool) {
	if role == "" || task == "" {
		return "spawn_subagent requires both 'role' and 'task'", true
	}
	id, err := randID()
	if err != nil {
		return "spawn_subagent: " + err.Error(), true
	}
	dir := filepath.Join(r.root, delegateDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "spawn_subagent: " + err.Error(), true
	}
	reqBody, _ := json.MarshalIndent(map[string]string{
		"id": id, "role": role, "task": task, "model": model,
	}, "", "  ")
	// Write to a temp name then rename so the watcher never reads a partial request.
	tmp := filepath.Join(dir, "request.json.tmp")
	final := filepath.Join(dir, "request.json")
	if err := os.WriteFile(tmp, reqBody, 0o644); err != nil {
		return "spawn_subagent: " + err.Error(), true
	}
	if err := os.Rename(tmp, final); err != nil {
		return "spawn_subagent: " + err.Error(), true
	}

	resPath := filepath.Join(dir, "result.json")
	deadline := time.Now().Add(r.delegateTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(resPath)
		if err != nil {
			time.Sleep(r.delegatePoll)
			continue
		}
		var res struct {
			Result   string `json:"result"`
			ExitCode int    `json:"exitCode"`
			Error    string `json:"error"`
		}
		if json.Unmarshal(data, &res) != nil {
			return "spawn_subagent: unreadable result", true
		}
		if res.Error != "" {
			return "sub-agent failed: " + res.Error, true
		}
		if res.Result == "" {
			return fmt.Sprintf("sub-agent finished (exit %d) with no summary.", res.ExitCode), res.ExitCode != 0
		}
		return res.Result, res.ExitCode != 0
	}
	return "sub-agent delegation timed out before a result was returned", true
}

func randID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
