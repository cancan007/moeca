package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orchestra/hostagent/internal/store"
)

// exampleSchedules are seeded once into a fresh database so the UI isn't empty.
// Seeding is guarded in the store, so user deletions are not resurrected.
func exampleSchedules() []*store.Schedule {
	return []*store.Schedule{
		{Name: "競合UI監視", Cron: "0 8 * * *", Perspective: "discovery", Task: "競合プロダクトのUI変更を収集して要約", Active: true},
		{Name: "コンテキスト最適化", Cron: "0 18 * * *", Perspective: "context-opt", Task: "システムプロンプトと履歴を圧縮・再構成", Active: true},
		{Name: "日次レポート配信", Cron: "30 9 * * 1-5", Perspective: "automation", Task: "日次サマリを生成して配信", Active: false},
	}
}

func (s *Server) handleSchedules(w http.ResponseWriter, _ *http.Request) {
	list, err := s.store.List()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []*store.Schedule{}
	}
	writeJSON(w, 200, map[string]any{"schedules": list})
}

type scheduleReq struct {
	ID            string            `json:"id"` // update only
	Name          string            `json:"name"`
	Cron          string            `json:"cron"`
	Perspective   string            `json:"perspective"`
	Task          string            `json:"task"`
	Active        *bool             `json:"active"`
	Goal          string            `json:"goal"`
	Milestones    []store.Milestone `json:"milestones"`
	TemplateLabel string            `json:"templateLabel"`
	TemplateRef   string            `json:"templateRef"`
	RunSpec       json.RawMessage   `json:"runSpec"`
}

// scheduleFromReq validates the request and builds a Schedule. active defaults
// to true when unspecified.
func scheduleFromReq(req *scheduleReq) (*store.Schedule, string) {
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Cron) == "" {
		return nil, "name and cron are required"
	}
	// Goal format: a goal must come with at least one milestone.
	if strings.TrimSpace(req.Goal) != "" {
		nonEmpty := 0
		for _, m := range req.Milestones {
			if strings.TrimSpace(m.Title) != "" {
				nonEmpty++
			}
		}
		if nonEmpty == 0 {
			return nil, "a goal requires at least one milestone"
		}
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	return &store.Schedule{
		ID:            req.ID,
		Name:          req.Name,
		Cron:          req.Cron,
		Perspective:   req.Perspective,
		Task:          req.Task,
		Active:        active,
		Goal:          req.Goal,
		Milestones:    req.Milestones,
		TemplateLabel: req.TemplateLabel,
		TemplateRef:   req.TemplateRef,
		RunSpec:       req.RunSpec,
	}, ""
}

func (s *Server) handleScheduleCreate(w http.ResponseWriter, r *http.Request) {
	var req scheduleReq
	if !decode(w, r, &req) {
		return
	}
	spec, errMsg := scheduleFromReq(&req)
	if errMsg != "" {
		writeErr(w, 400, errMsg)
		return
	}
	sc, err := s.store.Create(spec)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, sc)
}

// handleScheduleUpdate rewrites an existing schedule (used to re-sync a bound
// template's compiled run when its per-granularity prompt is edited).
func (s *Server) handleScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	var req scheduleReq
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		writeErr(w, 400, "id is required")
		return
	}
	spec, errMsg := scheduleFromReq(&req)
	if errMsg != "" {
		writeErr(w, 400, errMsg)
		return
	}
	sc, err := s.store.Update(spec)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if sc == nil {
		writeErr(w, 404, "unknown schedule")
		return
	}
	writeJSON(w, 200, sc)
}

func (s *Server) handleScheduleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	ok, err := s.store.Delete(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "unknown schedule")
		return
	}
	writeJSON(w, 200, map[string]string{"removed": id})
}

func (s *Server) handleScheduleToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	sc, err := s.store.Toggle(req.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if sc == nil {
		writeErr(w, 404, "unknown schedule")
		return
	}
	writeJSON(w, 200, sc)
}

// heartbeatKey tracks the last wall-clock time the app was running, so a
// restart can tell which scheduled occurrences were missed while it was down.
const heartbeatKey = "last_active"

// maxBackfill bounds the missed-occurrence scan (window and row count) so a long
// downtime — or a per-minute cron — cannot produce an unbounded backfill.
const (
	maxBackfillDays = 14
	maxBackfillRows = 1000
)

// startScheduler launches the per-minute cron tick plus a faster heartbeat so
// the "app was running" window is tracked at finer granularity than one minute.
func (s *Server) startScheduler() {
	go func() {
		cron := time.NewTicker(time.Minute)
		beat := time.NewTicker(30 * time.Second)
		defer cron.Stop()
		defer beat.Stop()
		for {
			select {
			case now := <-cron.C:
				s.tickSchedules(now)
			case now := <-beat.C:
				_ = s.store.SetState(heartbeatKey, now.Format(time.RFC3339))
			}
		}
	}()
}

// tickSchedules records an 'executed' occurrence for each active, matching
// schedule. It is guarded so it fires at most once per wall-clock minute.
func (s *Server) tickSchedules(now time.Time) {
	minute := now.Truncate(time.Minute)
	s.ltMu.Lock()
	if !s.lastTick.IsZero() && s.lastTick.Equal(minute) {
		s.ltMu.Unlock()
		return
	}
	s.lastTick = minute
	s.ltMu.Unlock()

	_ = s.store.SetState(heartbeatKey, now.Format(time.RFC3339))

	list, err := s.store.List()
	if err != nil {
		log.Printf("hostagent: tick list: %v", err)
		return
	}
	for _, sc := range list {
		if sc.Active && cronMatches(sc.Cron, now) {
			if _, err := s.store.RecordRun(sc.ID, now); err != nil {
				log.Printf("hostagent: record run %s: %v", sc.ID, err)
			}
			// A schedule with a bound template launches a real agent run;
			// otherwise the occurrence is recorded for status tracking only.
			// What gates this is the template, not a repository — a Daily run
			// needs somewhere to write, and it brings its own directory.
			occ := store.ScheduleRun{
				ScheduleID: sc.ID, Name: sc.Name, Perspective: sc.Perspective, Status: store.RunStatusExecuted,
			}
			if len(sc.RunSpec) > 0 {
				dir, cid, runID, err := s.launchScheduledRun(sc, now)
				occ.OutputDir, occ.ContainerID, occ.RunID, occ.Template = dir, cid, runID, sc.TemplateLabel
				if err != nil {
					log.Printf("hostagent: launch scheduled run %s: %v", sc.ID, err)
					occ.Status = store.RunStatusFailed
				}
			}
			_ = s.store.RecordOccurrence(occ, now)
		}
	}
}

// dailyRoot is where scheduled runs write their artifacts, one directory per
// occurrence. It sits under the app data dir when there is one, so artifacts
// live alongside the database and survive restarts; tests and a dataDir-less
// install fall back to a temp root.
//
// Both roots are also paths Docker Desktop will bind-mount on macOS (under
// $HOME or /var/folders); a directory outside those roots would be silently
// refused and every scheduled run would fail to start.
func (s *Server) dailyRoot() string {
	if s.cfg.DataDir != "" {
		return filepath.Join(s.cfg.DataDir, "daily")
	}
	return filepath.Join(os.TempDir(), "orchestra-daily")
}

// launchScheduledRun starts a schedule's agent template through the orchestrator
// and returns the directory its artifacts land in, plus the run id.
//
// Daily runs are not git work. A schedule produces a report, a rendered video, a
// chart — things reviewed and downloaded from the gallery, not diffed and
// merged. It therefore gets a plain output directory of its own rather than a
// worktree, and needs no repository to be configured at all: a schedule that
// never touches code should not be blocked by a missing repo, and its output
// should not arrive as an unmergeable branch nobody asked for.
//
// containerID is always empty and is kept only so existing occurrence rows —
// written back when a bare single-agent path still existed — keep their shape.
func (s *Server) launchScheduledRun(sc *store.Schedule, now time.Time) (outputDir, containerID, runID string, err error) {
	// Every schedule runs its compiled template DAG through the orchestrator. A
	// single agent is a one-stage template, so there is no bare-container
	// fallback: the old one ignored the bound agent's model, system prompt and
	// tools, and bypassed run-scoped naming and the stage-log archive.
	if len(sc.RunSpec) == 0 {
		return "", "", "", fmt.Errorf("schedule %s has no agent template bound", sc.ID)
	}
	outputDir = filepath.Join(s.dailyRoot(), sanitize(sc.ID), strconv.FormatInt(now.Unix(), 10))
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("creating output dir: %w", err)
	}
	taskID := sanitize(sc.ID + "-" + strconv.FormatInt(now.Unix(), 10))
	runID, err = s.startRun(taskID, outputDir, sc.RunSpec)
	return outputDir, "", runID, err
}

// startRun submits a compiled run spec (stages DAG) to the sandbox controller's
// orchestrator, injecting the task id + worktree, and returns the run id.
func (s *Server) startRun(taskID, worktree string, spec json.RawMessage) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(spec, &m); err != nil {
		return "", fmt.Errorf("bad runSpec: %w", err)
	}
	m["taskId"] = taskID
	m["worktreePath"] = worktree
	// Every run started here is a schedule firing with nobody watching, so the
	// controller must apply its unattended image restrictions. Forcing the flag
	// here rather than trusting the stored spec matters twice over: schedules
	// compiled before the flag existed carry no value at all, and a stored spec
	// is not the authority on whether a human is present — this code path is.
	m["unattended"] = true
	// Same argument for how the stages share files. A Daily run writes into a
	// plain output directory, not a git worktree, so the orchestrator cannot cut
	// a branch per stage and "isolated" is not available to it; that leaves one
	// shared directory, which two concurrent stages would write over. The
	// orchestrator refuses that combination, so a stored spec asking for it is
	// rejected with a 400 before any container starts — which is how every
	// scheduled run used to fail. This code path knows the worktree is plain, so
	// it states the only valid arrangement rather than hoping the spec does.
	m["worktreeMode"] = "shared"
	m["maxParallel"] = 1
	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(s.cfg.sandboxURL(), "/")+"/run", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("run %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		RunID string `json:"runId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.RunID, nil
}

// backfillMissed records a 'missed' occurrence for every active schedule whose
// cron fell between the last recorded heartbeat and now (i.e. while the app was
// not running). First run just seeds the heartbeat. The scan is bounded by
// maxBackfillDays / maxBackfillRows.
func (s *Server) backfillMissed(now time.Time) {
	last, _ := s.store.GetState(heartbeatKey)
	if last == "" {
		_ = s.store.SetState(heartbeatKey, now.Format(time.RFC3339))
		return
	}
	from, err := time.Parse(time.RFC3339, last)
	if err != nil {
		_ = s.store.SetState(heartbeatKey, now.Format(time.RFC3339))
		return
	}
	if floor := now.Add(-maxBackfillDays * 24 * time.Hour); from.Before(floor) {
		from = floor
	}
	list, err := s.store.List()
	if err != nil {
		return
	}
	rows := 0
	end := now.Truncate(time.Minute)
	for m := from.Truncate(time.Minute).Add(time.Minute); m.Before(end) && rows < maxBackfillRows; m = m.Add(time.Minute) {
		for _, sc := range list {
			if sc.Active && cronMatches(sc.Cron, m) {
				if err := s.store.RecordOccurrence(store.ScheduleRun{
					ScheduleID: sc.ID, Name: sc.Name, Perspective: sc.Perspective, Status: store.RunStatusMissed,
				}, m); err == nil {
					rows++
				}
			}
		}
	}
	if rows > 0 {
		log.Printf("hostagent: backfilled %d missed occurrence(s) since %s", rows, last)
	}
	_ = s.store.SetState(heartbeatKey, now.Format(time.RFC3339))
}

// cronMatches reports whether the 5-field cron expr ("m h dom mon dow") matches
// t. Each field may be "*", a number N, a step "*/n", or a comma list "a,b,c".
// Any unsupported field (e.g. a range "1-5") yields no match rather than a crash.
// The time is passed in so the function is deterministic (never calls time.Now).
func cronMatches(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	vals := [5]int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i, f := range fields {
		if !cronFieldMatches(f, vals[i]) {
			return false
		}
	}
	return true
}

func cronFieldMatches(field string, v int) bool {
	if field == "*" {
		return true
	}
	if strings.Contains(field, ",") {
		for _, part := range strings.Split(field, ",") {
			if cronFieldMatches(part, v) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(field, "*/") {
		n, err := strconv.Atoi(field[2:])
		if err != nil || n <= 0 {
			return false
		}
		return v%n == 0
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return false // unsupported token (e.g. a range) => no match
	}
	return v == n
}
