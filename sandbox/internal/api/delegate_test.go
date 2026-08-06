package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orchestra/sandbox/internal/docker"
)

// TestDelegation_ProcessPending drives one delegation request through the
// controller: the fake "sub-agent" writes a summary, and processPending must run
// it as a hardened container in the SAME worktree and publish result.json.
func TestDelegation_ProcessPending(t *testing.T) {
	wt := t.TempDir()

	fake := &fakeDocker{}
	fake.createHook = func(spec docker.Spec) {
		// simulate the sub-agent leaving its summary in the worktree
		os.MkdirAll(filepath.Join(spec.WorktreePath, ".orchestra"), 0o755)
		os.WriteFile(filepath.Join(spec.WorktreePath, subagentResultFile), []byte("did the work\n"), 0o644)
	}
	s := newWith(&Config{Image: "img", GatewayStrictBase: "http://gw", SessionToken: "sess"}, fake)

	run := &Run{ID: "run-x", TaskID: "t", maxDepth: 1}
	stage := Stage{ID: "sup", Provider: "anthropic", ProviderPrefix: "/anthropic/"}

	base := filepath.Join(wt, delegateSubdir)
	dir := filepath.Join(base, "abc")
	os.MkdirAll(dir, 0o755)
	reqBody, _ := json.Marshal(delegateRequest{ID: "abc", Role: "builder", Task: "build the widget"})
	os.WriteFile(filepath.Join(dir, "request.json"), reqBody, 0o644)

	s.processPending(base, wt, run, stage, ImagePolicy{Name: "base", Ref: "img"}, true)

	// result.json published with the sub-agent's summary + clean exit
	resRaw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatalf("result.json not written: %v", err)
	}
	var res struct {
		Result   string `json:"result"`
		ExitCode int    `json:"exitCode"`
		Error    string `json:"error"`
	}
	json.Unmarshal(resRaw, &res)
	if res.Error != "" || res.ExitCode != 0 || res.Result != "did the work" {
		t.Fatalf("result = %+v, want {did the work, 0}", res)
	}

	// the child container mounted the SAME worktree, on the strict egress net,
	// as a depth-1 sub-stage that itself cannot delegate
	fake.mu.Lock()
	spec := fake.created[len(fake.created)-1]
	fake.mu.Unlock()
	if spec.WorktreePath != wt {
		t.Errorf("child worktree = %q, want the parent's %q", spec.WorktreePath, wt)
	}
	if spec.Env["ORCHESTRA_STAGE"] != "sup-sub-abc" {
		t.Errorf("child stage attribution = %q", spec.Env["ORCHESTRA_STAGE"])
	}
	if spec.Env["ORCHESTRA_DELEGATE_DEPTH"] != "1" {
		t.Errorf("child depth = %q, want 1 (cannot re-delegate)", spec.Env["ORCHESTRA_DELEGATE_DEPTH"])
	}
	if spec.Env["ORCHESTRA_SESSION"] != "sess" {
		t.Errorf("child must inherit the forced session token, got %q", spec.Env["ORCHESTRA_SESSION"])
	}

	// idempotent: a second sweep does not re-run the fulfilled request
	before := len(fake.created)
	s.processPending(base, wt, run, stage, ImagePolicy{Name: "base", Ref: "img"}, true)
	if len(fake.created) != before {
		t.Errorf("fulfilled request was re-run: %d -> %d creates", before, len(fake.created))
	}
}

// TestDelegation_CreateErrorReported ensures a failed sub-agent launch is
// surfaced to the caller via result.json rather than hanging.
func TestDelegation_CreateErrorReported(t *testing.T) {
	wt := t.TempDir()
	fake := &fakeDocker{createErr: errors.New("launch failed")}
	s := newWith(&Config{Image: "img", GatewayStrictBase: "http://gw"}, fake)

	base := filepath.Join(wt, delegateSubdir)
	dir := filepath.Join(base, "z")
	os.MkdirAll(dir, 0o755)
	reqBody, _ := json.Marshal(delegateRequest{ID: "z", Role: "r", Task: "t"})
	os.WriteFile(filepath.Join(dir, "request.json"), reqBody, 0o644)

	s.processPending(base, wt, &Run{ID: "r", TaskID: "t", maxDepth: 1}, Stage{ID: "s"}, ImagePolicy{Name: "base", Ref: "img"}, true)

	resRaw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatalf("result.json not written on error: %v", err)
	}
	var res struct{ Error string `json:"error"` }
	json.Unmarshal(resRaw, &res)
	if res.Error == "" {
		t.Error("expected an error in result.json when the child fails to launch")
	}
}
