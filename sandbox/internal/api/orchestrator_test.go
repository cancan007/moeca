package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// startRun POSTs a run and returns its id.
func startRun(t *testing.T, srv *httptest.Server, body map[string]any) (string, int) {
	t.Helper()
	resp, out := do(t, srv, "POST", "/run", body)
	id, _ := out["runId"].(string)
	return id, resp.StatusCode
}

// waitRun polls GET /run until the run reaches a terminal status or times out.
func waitRun(t *testing.T, srv *httptest.Server, id string) map[string]any {
	t.Helper()
	for i := 0; i < 400; i++ {
		_, out := do(t, srv, "GET", "/run?id="+id, nil)
		if st, _ := out["status"].(string); st != "" && st != statusRunning {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish in time", id)
	return nil
}

func stageStatuses(run map[string]any) map[string]string {
	res := map[string]string{}
	stages, _ := run["stages"].([]any)
	for _, s := range stages {
		m, _ := s.(map[string]any)
		id, _ := m["id"].(string)
		st, _ := m["status"].(string)
		res[id] = st
	}
	return res
}

// createdStageOrder returns the stage ids in Create call order (taskID is "t-<id>").
func createdStageOrder(f *fakeDocker) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var order []string
	for _, spec := range f.created {
		order = append(order, strings.TrimPrefix(spec.TaskID, "t-"))
	}
	return order
}

func chainReq(stages ...map[string]any) map[string]any {
	return map[string]any{"taskId": "t", "worktreePath": "/w", "stages": stages}
}

func stage(id string, deps ...string) map[string]any {
	m := map[string]any{"id": id, "name": id, "role": id}
	if len(deps) > 0 {
		m["dependsOn"] = deps
	}
	return m
}

func TestRun_Chain(t *testing.T) {
	fake := &fakeDocker{}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	id, code := startRun(t, srv, chainReq(stage("a"), stage("b", "a"), stage("c", "b")))
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusDone {
		t.Errorf("run status = %v, want done", run["status"])
	}
	for _, id := range []string{"a", "b", "c"} {
		if st := stageStatuses(run)[id]; st != statusDone {
			t.Errorf("stage %s = %s, want done", id, st)
		}
	}
	if got := createdStageOrder(fake); strings.Join(got, ",") != "a,b,c" {
		t.Errorf("create order = %v, want [a b c]", got)
	}
}

func TestRun_BranchParallel(t *testing.T) {
	// Parallel stages need isolated worktrees: in shared mode they would write
	// over each other in the one tree, which the API now rejects outright.
	base := t.TempDir()
	gitInit(t, base)

	fake := &fakeDocker{}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	// a -> {b, c} -> d
	body := map[string]any{
		"taskId": "t", "worktreePath": base, "maxParallel": 2, "worktreeMode": "isolated",
		"stages": []map[string]any{stage("a"), stage("b", "a"), stage("c", "a"), stage("d", "b", "c")},
	}
	id, code := startRun(t, srv, body)
	if code != 201 {
		t.Fatalf("create status = %d, want 201", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusDone {
		t.Errorf("run status = %v, want done", run["status"])
	}
	order := createdStageOrder(fake)
	if len(order) != 4 || order[0] != "a" || order[3] != "d" {
		t.Errorf("create order = %v, want a first and d last", order)
	}
}

func TestRun_DependencyFailureSkips(t *testing.T) {
	fake := &fakeDocker{waitFn: func(taskID string) (int, error) {
		if strings.HasSuffix(taskID, "-a") {
			return 1, nil // a fails
		}
		return 0, nil
	}}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	id, _ := startRun(t, srv, chainReq(stage("a"), stage("b", "a"), stage("c", "b")))
	run := waitRun(t, srv, id)
	if run["status"] != statusFailed {
		t.Errorf("run status = %v, want failed", run["status"])
	}
	ss := stageStatuses(run)
	if ss["a"] != statusFailed {
		t.Errorf("stage a = %s, want failed", ss["a"])
	}
	if ss["b"] != statusSkipped || ss["c"] != statusSkipped {
		t.Errorf("downstream not skipped: b=%s c=%s", ss["b"], ss["c"])
	}
}

func TestRun_CycleRejected(t *testing.T) {
	fake := &fakeDocker{}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()
	// a -> b -> a
	_, code := startRun(t, srv, chainReq(stage("a", "b"), stage("b", "a")))
	if code != 400 {
		t.Errorf("cyclic graph status = %d, want 400", code)
	}
}

func TestRun_UnknownDependencyRejected(t *testing.T) {
	fake := &fakeDocker{}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()
	_, code := startRun(t, srv, chainReq(stage("a", "ghost")))
	if code != 400 {
		t.Errorf("unknown dep status = %d, want 400", code)
	}
}

func TestRun_Stop(t *testing.T) {
	release := make(chan struct{})
	fake := &fakeDocker{waitFn: func(taskID string) (int, error) {
		if strings.HasSuffix(taskID, "-a") {
			<-release // block until the test triggers stop
			return 137, nil
		}
		return 0, nil
	}}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	id, _ := startRun(t, srv, chainReq(stage("a"), stage("b", "a")))

	// Wait until stage a is running, then stop.
	for i := 0; i < 200; i++ {
		_, out := do(t, srv, "GET", "/run?id="+id, nil)
		if stageStatuses(out)["a"] == statusRunning {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	do(t, srv, "POST", "/run/stop", map[string]any{"id": id})
	close(release)

	run := waitRun(t, srv, id)
	if run["status"] != statusStopped {
		t.Errorf("run status = %v, want stopped", run["status"])
	}
	if len(fake.stopped) == 0 {
		t.Errorf("expected the running container to be stopped")
	}
}

func TestRun_ValidationErrors(t *testing.T) {
	fake := &fakeDocker{}
	srv := newTest(&Config{Image: "img"}, fake)
	defer srv.Close()

	if _, code := startRun(t, srv, map[string]any{"worktreePath": "/w", "stages": []map[string]any{stage("a")}}); code != 400 {
		t.Errorf("missing taskId = %d, want 400", code)
	}
	if _, code := startRun(t, srv, map[string]any{"taskId": "t", "stages": []map[string]any{stage("a")}}); code != 400 {
		t.Errorf("missing worktreePath = %d, want 400", code)
	}
	if _, code := startRun(t, srv, map[string]any{"taskId": "t", "worktreePath": "/w", "stages": []map[string]any{}}); code != 400 {
		t.Errorf("empty stages = %d, want 400", code)
	}
}
