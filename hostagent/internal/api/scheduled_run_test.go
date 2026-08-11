package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestra/hostagent/internal/store"
)

// dailyServer builds a host agent whose scheduled runs write into a temp data
// dir and whose orchestrator calls go to h.
func dailyServer(t *testing.T, h http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	sb := httptest.NewServer(h)
	t.Cleanup(sb.Close)
	s := New(&Config{NoSeed: true, DataDir: t.TempDir(), Sandbox: SandboxConfig{URL: sb.URL}})
	return s, sb
}

// A schedule is a Daily job, not git work: it needs no repository, and one must
// not be required for it to run. The whole point of separating the two is that
// a schedule producing a report or a video has nothing to bind a repo to.
func TestScheduledRunNeedsNoRepository(t *testing.T) {
	var runBody map[string]any
	s, _ := dailyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/run" {
			json.NewDecoder(r.Body).Decode(&runBody)
			w.WriteHeader(201)
			w.Write([]byte(`{"runId":"r1"}`))
			return
		}
		w.WriteHeader(500) // every schedule must use /run
	})
	s.store.Create(&store.Schedule{
		Name: "daily-video", Cron: "0 3 * * *", Active: true,
		TemplateLabel: "Graph — Media",
		RunSpec:       []byte(`{"stages":[{"id":"render","name":"Render"}],"maxParallel":2}`),
	})

	s.tickSchedules(time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC))

	if runBody == nil {
		t.Fatal("orchestrator /run was not called — no repository should be needed")
	}
	if _, ok := runBody["taskId"]; !ok {
		t.Fatalf("taskId not injected: %v", runBody)
	}
	// The run writes into a Daily output directory, not a worktree.
	wt, _ := runBody["worktreePath"].(string)
	if !strings.Contains(filepath.ToSlash(wt), "/daily/") {
		t.Errorf("worktreePath = %q, want a Daily output directory", wt)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Errorf("output directory was not created at %q: %v", wt, err)
	}

	runs, _ := s.store.Runs(0)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want 1", runs)
	}
	if runs[0].RunID != "r1" || runs[0].Template != "Graph — Media" {
		t.Errorf("occurrence = %+v, want runId r1 + template", runs[0])
	}
	// The occurrence records where the artifacts are, which is what the gallery
	// resolves an id to.
	if runs[0].OutputDir != wt {
		t.Errorf("occurrence outputDir = %q, want %q", runs[0].OutputDir, wt)
	}
}

// What decides whether a fired schedule launches anything is the bound
// template. Without one there is nothing to run, so the occurrence is recorded
// for status tracking and no container is started.
func TestScheduledRunWithoutTemplateRecordsStatusOnly(t *testing.T) {
	called := false
	s, _ := dailyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(201)
		w.Write([]byte(`{"runId":"x"}`))
	})
	s.store.Create(&store.Schedule{Name: "reminder", Cron: "0 3 * * *", Active: true, Goal: "triage"})

	s.tickSchedules(time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC))

	if called {
		t.Fatal("an unbound schedule must not launch anything")
	}
	runs, _ := s.store.Runs(0)
	if len(runs) != 1 || runs[0].Status != store.RunStatusExecuted {
		t.Fatalf("runs = %+v, want 1 executed (status-only)", runs)
	}
	if runs[0].OutputDir != "" {
		t.Errorf("outputDir = %q, want empty when nothing ran", runs[0].OutputDir)
	}
}

func TestScheduledRunFailureRecordsFailed(t *testing.T) {
	s, _ := dailyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	})
	s.store.Create(&store.Schedule{
		Name: "n", Cron: "0 3 * * *", Active: true,
		RunSpec: []byte(`{"stages":[{"id":"plan","name":"Plan"}]}`),
	})

	s.tickSchedules(time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC))
	runs, _ := s.store.Runs(0)
	if len(runs) != 1 || runs[0].Status != store.RunStatusFailed {
		t.Fatalf("runs = %+v, want 1 failed", runs)
	}
}

// Each occurrence gets its own directory, so two runs of the same schedule
// cannot overwrite each other's artifacts.
func TestScheduledRunsGetSeparateOutputDirectories(t *testing.T) {
	s, _ := dailyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"runId":"r"}`))
	})
	s.store.Create(&store.Schedule{
		Name: "hourly", Cron: "0 * * * *", Active: true,
		RunSpec: []byte(`{"stages":[{"id":"a"}]}`),
	})

	s.tickSchedules(time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC))
	s.tickSchedules(time.Date(2026, 7, 8, 4, 0, 0, 0, time.UTC))

	runs, _ := s.store.Runs(0)
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	if runs[0].OutputDir == runs[1].OutputDir {
		t.Errorf("both occurrences share %q", runs[0].OutputDir)
	}
}

func TestScheduleUpdateResyncsRunSpec(t *testing.T) {
	srv := httptest.NewServer(New(&Config{NoSeed: true}).Handler())
	defer srv.Close()

	resp, body := req(t, srv, "POST", "/schedules", map[string]any{
		"name": "s", "cron": "0 9 * * *",
		"templateLabel": "Solo — Coder", "runSpec": map[string]any{"stages": []any{}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	id := body["id"].(string)

	// update: new template + runSpec (the re-sync after a prompt edit)
	resp, body = req(t, srv, "POST", "/schedules/update", map[string]any{
		"id": id, "name": "s", "cron": "0 9 * * *",
		"templateLabel": "Solo — Coder v2",
		"runSpec":       map[string]any{"stages": []any{map[string]any{"id": "x", "system": "improved"}}},
	})
	if resp.StatusCode != 200 || body["templateLabel"] != "Solo — Coder v2" {
		t.Fatalf("update: %d %v", resp.StatusCode, body)
	}
	// unknown id => 404
	resp, _ = req(t, srv, "POST", "/schedules/update", map[string]any{"id": "sch-999", "name": "n", "cron": "* * * * *"})
	if resp.StatusCode != 404 {
		t.Fatalf("update unknown: %d, want 404", resp.StatusCode)
	}
}

// A schedule stored before this was understood asks for a shared worktree with
// concurrency, which the orchestrator refuses outright — so the run is rejected
// with a 400 and no container ever starts. The firing path knows the worktree
// is a plain directory, so it states the only workable arrangement rather than
// forwarding whatever was stored.
func TestScheduledRunOverridesAnUnrunnableStoredSpec(t *testing.T) {
	var body map[string]any
	s, _ := dailyServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(201)
		w.Write([]byte(`{"runId":"r1"}`))
	})
	s.store.Create(&store.Schedule{
		Name: "legacy", Cron: "0 3 * * *", Active: true,
		RunSpec: []byte(`{"stages":[{"id":"a"}],"worktreeMode":"shared","maxParallel":2}`),
	})

	s.tickSchedules(time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC))

	if body == nil {
		t.Fatal("orchestrator was not called")
	}
	if body["worktreeMode"] != "shared" {
		t.Errorf("worktreeMode = %v, want shared", body["worktreeMode"])
	}
	if n, _ := body["maxParallel"].(float64); n != 1 {
		t.Errorf("maxParallel = %v, want 1 — a shared directory cannot take concurrent stages", body["maxParallel"])
	}
	if body["unattended"] != true {
		t.Errorf("unattended = %v, want true", body["unattended"])
	}
}

// Running a schedule by hand.
//
// Testing one used to mean editing its cron to a minute in the near future and
// waiting — a race, because the per-minute tick is aligned to when the process
// started rather than to the wall clock. A schedule saved three seconds after
// its minute's tick simply does not fire, and nothing says why.

func postRun(t *testing.T, srv *httptest.Server, id string) (int, map[string]any) {
	t.Helper()
	res, err := http.Post(srv.URL+"/schedules/run", "application/json",
		strings.NewReader(`{"id":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestRunNowLaunchesWithoutWaitingForCron(t *testing.T) {
	var called bool
	s, _ := dailyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/run" {
			called = true
			w.WriteHeader(201)
			w.Write([]byte(`{"runId":"r-manual"}`))
			return
		}
		w.WriteHeader(500)
	})
	sc, _ := s.store.Create(&store.Schedule{
		Name: "daily", Cron: "0 3 * * *", Active: true,
		RunSpec: []byte(`{"stages":[{"id":"a"}]}`),
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	code, out := postRun(t, srv, sc.ID)
	if code != 202 {
		t.Fatalf("status = %d, want 202", code)
	}
	if !called {
		t.Fatal("the orchestrator was not called")
	}
	if out["runId"] != "r-manual" {
		t.Errorf("runId = %v", out["runId"])
	}
	// It belongs in the history like any other run: that is where the operator
	// looks for what it produced.
	runs, _ := s.store.Runs(0)
	if len(runs) != 1 || runs[0].RunID != "r-manual" {
		t.Fatalf("occurrence not recorded: %+v", runs)
	}
}

// Pausing stops the clock from firing a schedule. Running it by hand is exactly
// what someone does before turning the clock back on.
func TestRunNowWorksOnAPausedSchedule(t *testing.T) {
	s, _ := dailyServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"runId":"r1"}`))
	})
	sc, _ := s.store.Create(&store.Schedule{
		Name: "paused", Cron: "0 3 * * *", Active: false,
		RunSpec: []byte(`{"stages":[{"id":"a"}]}`),
	})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	if code, _ := postRun(t, srv, sc.ID); code != 202 {
		t.Errorf("status = %d, want 202 for a paused schedule", code)
	}
}

func TestRunNowRefusesASchedulesWithNoTemplate(t *testing.T) {
	s, _ := dailyServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) })
	sc, _ := s.store.Create(&store.Schedule{Name: "bare", Cron: "0 3 * * *", Active: true})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	code, out := postRun(t, srv, sc.ID)
	if code != 400 {
		t.Fatalf("status = %d, want 400", code)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "template") {
		t.Errorf("error = %q, want it to name the missing template", msg)
	}
}

func TestRunNowRejectsAnUnknownSchedule(t *testing.T) {
	s, _ := dailyServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(201) })
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	if code, _ := postRun(t, srv, "nope"); code != 404 {
		t.Errorf("status = %d, want 404", code)
	}
}
