// Package api exposes the host agent's HTTP surface consumed by the Orchestra
// frontend: list worktree-backed tasks, fetch real diffs and file contents, run
// the CI gate, and merge on self-review approval.
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestra/hostagent/internal/diff"
	"orchestra/hostagent/internal/git"
	"orchestra/hostagent/internal/githubapp"
	"orchestra/hostagent/internal/store"
	"orchestra/hostagent/internal/tasksource"
)

// Repo is a configured repository the agent manages worktrees for.
type Repo struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`   // path to the main repo (or bare) directory
	Target    string   `json:"target"` // default merge target branch, e.g. "main"
	CICommand []string `json:"ciCommand"`
}

// Config is the host agent configuration.
type Config struct {
	Listen string `json:"listen"`
	Repos  []Repo `json:"repos"`
	// DataDir is where the SQLite database lives. Empty => in-memory (tests).
	DataDir string `json:"dataDir"`
	// NoSeed skips seeding example schedules (for clean org deployments).
	NoSeed bool `json:"noSeed"`
	// DemoSources registers a network-free demo task source for the Daily
	// pull flow when no real providers are configured yet.
	DemoSources bool `json:"demoSources"`
	// Gateway is how host-side task-source adapters reach the security gateway
	// (they route reads through it; the gateway injects upstream credentials).
	Gateway GatewayConfig `json:"gateway"`
	// TaskSources are the Daily pull providers (jira / trello / notion).
	TaskSources []TaskSourceConfig `json:"taskSources"`
	// Sandbox is the loopback sandbox controller a fired schedule launches its
	// agent run through. Empty defaults to http://127.0.0.1:8789.
	Sandbox SandboxConfig `json:"sandbox"`
}

// SandboxConfig points scheduled agent runs at the sandbox controller.
type SandboxConfig struct {
	URL string `json:"url"`
}

// sandboxURL returns the configured sandbox controller URL or the loopback default.
func (c *Config) sandboxURL() string {
	if c.Sandbox.URL != "" {
		return c.Sandbox.URL
	}
	return "http://127.0.0.1:8789"
}

// GatewayConfig points the task-source adapters at the gateway.
type GatewayConfig struct {
	URL     string `json:"url"`     // e.g. http://127.0.0.1:8787
	Session string `json:"session"` // an ORCHESTRA_SESSION token the gateway accepts
}

// TaskSourceConfig configures one Daily pull provider.
type TaskSourceConfig struct {
	Type string `json:"type"` // jira | trello | notion
	Name string `json:"name"` // display name; defaults to Type
}

// Task is a worktree awaiting review (branch != target).
type Task struct {
	ID           string `json:"id"`
	Repo         string `json:"repo"`
	Branch       string `json:"branch"`
	Target       string `json:"target"`
	WorktreePath string `json:"worktreePath"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Files        int    `json:"files"`
	CI           string `json:"ci"` // none | running | passed | failed
}

// Server implements the HTTP API.
type Server struct {
	cfg *Config
	g   *git.Runner

	mu sync.Mutex
	ci map[string]string // "repo/branch" -> ci status

	store *store.SQLiteStore

	srcMu   sync.RWMutex // guards sources
	sources *tasksource.Registry

	ghMu  sync.RWMutex     // guards ghApp
	ghApp *githubapp.App   // host-side GitHub App for direct issue pulls (optional)

	ltMu     sync.Mutex // guards lastTick
	lastTick time.Time  // minute of the last scheduler tick (fire-once guard)
}

// currentSources returns the live registry under the read lock.
func (s *Server) currentSources() *tasksource.Registry {
	s.srcMu.RLock()
	defer s.srcMu.RUnlock()
	return s.sources
}

// New builds a Server. An empty cfg.DataDir opens an in-memory database (tests);
// a real path persists it. Store initialisation never fails the constructor: on
// error it falls back to an in-memory database and logs.
func New(cfg *Config) *Server {
	path := ":memory:"
	if cfg.DataDir != "" {
		if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
			log.Printf("hostagent: data dir %s: %v", cfg.DataDir, err)
		} else {
			path = filepath.Join(cfg.DataDir, "hostagent.db")
		}
	}
	st, err := store.Open(path)
	if err != nil {
		log.Printf("hostagent: open store %s: %v; using in-memory", path, err)
		if st, err = store.Open(":memory:"); err != nil {
			panic(err) // in-memory open should never fail
		}
	}
	if !cfg.NoSeed {
		if err := st.SeedOnce(exampleSchedules()); err != nil {
			log.Printf("hostagent: seed schedules: %v", err)
		}
	}
	s := &Server{cfg: cfg, g: git.New(), ci: map[string]string{}, store: st}
	s.rebuildSources()
	return s
}

// rebuildSources composes the task-source registry from the config file plus
// the runtime-configured providers persisted in the store, and swaps it in
// under the write lock. Called at startup and whenever sources change.
func (s *Server) rebuildSources() {
	specs := make([]store.TaskSourceRow, 0, len(s.cfg.TaskSources))
	for _, ts := range s.cfg.TaskSources {
		specs = append(specs, store.TaskSourceRow{Name: ts.Name, Type: ts.Type})
	}
	if rows, err := s.store.TaskSources(); err != nil {
		log.Printf("hostagent: load task sources: %v", err)
	} else {
		specs = append(specs, rows...)
	}

	var built []tasksource.Source
	if s.cfg.DemoSources {
		built = append(built, tasksource.NewStatic("demo",
			tasksource.Ticket{ID: "demo:1", Source: "demo", Title: "Notion: 仕様まとめ", State: "open", UpdatedAt: "2026-07-10T00:00:00Z"},
			tasksource.Ticket{ID: "demo:2", Source: "demo", Title: "Jira: バグ修正 PROJ-42", State: "open", UpdatedAt: "2026-07-11T00:00:00Z"},
		))
	}
	for _, ts := range specs {
		name := ts.Name
		if name == "" {
			name = ts.Type
		}
		gw := tasksource.NewGatewayClient(s.cfg.Gateway.URL, s.cfg.Gateway.Session)
		switch ts.Type {
		case "jira":
			built = append(built, tasksource.NewJira(name, gw))
		case "trello":
			built = append(built, tasksource.NewTrello(name, gw))
		case "notion":
			built = append(built, tasksource.NewNotion(name, gw))
		default:
			log.Printf("hostagent: unknown task source type %q (skipped)", ts.Type)
		}
	}

	// GitHub is the Delivery source (issues -> worktrees), registered whenever
	// the gateway is configured. It is kept out of the Daily source list.
	if s.cfg.Gateway.URL != "" {
		built = append(built, tasksource.NewGitHub(deliverySource, tasksource.NewGatewayClient(s.cfg.Gateway.URL, s.cfg.Gateway.Session)))
	}

	reg := tasksource.NewRegistry(built...)
	s.srcMu.Lock()
	s.sources = reg
	s.srcMu.Unlock()
}

// deliverySource is the source name for GitHub issues (Delivery, not Daily).
const deliverySource = "github"

// activeRepos returns the effective repository set: store-managed repos (added
// via Settings, authoritative) merged with the read-only config-file seeds. A
// store repo shadows a config seed of the same name. The store is queried live,
// so a runtime add/remove takes effect on the next request with no restart.
func (s *Server) activeRepos() []Repo {
	seen := map[string]bool{}
	var out []Repo
	if s.store != nil {
		if rows, err := s.store.Repos(); err == nil {
			for _, r := range rows {
				out = append(out, Repo{Name: r.Name, Path: r.Path, Target: r.Target, CICommand: r.CICommand})
				seen[r.Name] = true
			}
		}
	}
	for _, r := range s.cfg.Repos {
		if !seen[r.Name] {
			out = append(out, r)
		}
	}
	return out
}

func (s *Server) repo(name string) (Repo, bool) {
	for _, r := range s.activeRepos() {
		if r.Name == name {
			return r, true
		}
	}
	return Repo{}, false
}

func key(repo, branch string) string { return repo + "/" + branch }

func (s *Server) ciStatus(repo, branch string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.ci[key(repo, branch)]; ok {
		return st
	}
	return "none"
}

func (s *Server) setCI(repo, branch, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ci[key(repo, branch)] = status
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /tasks", s.handleTasks)
	mux.HandleFunc("POST /task", s.handleCreate)
	mux.HandleFunc("DELETE /task", s.handleDelete)
	mux.HandleFunc("GET /task/diff", s.handleDiff)
	mux.HandleFunc("GET /task/file", s.handleFile)
	mux.HandleFunc("POST /task/file", s.handleFileWrite)
	mux.HandleFunc("GET /task/files", s.handleFiles)
	mux.HandleFunc("GET /task/artifacts", s.handleTaskArtifacts)
	mux.HandleFunc("GET /task/artifact", s.handleTaskArtifact)
	mux.HandleFunc("POST /task/ci", s.handleCI)
	mux.HandleFunc("POST /task/merge", s.handleMerge)
	mux.HandleFunc("POST /task/pr", s.handlePullRequest)
	mux.HandleFunc("GET /schedules", s.handleSchedules)
	mux.HandleFunc("POST /schedules", s.handleScheduleCreate)
	mux.HandleFunc("POST /schedules/update", s.handleScheduleUpdate)
	mux.HandleFunc("DELETE /schedules", s.handleScheduleDelete)
	mux.HandleFunc("POST /schedules/toggle", s.handleScheduleToggle)
	mux.HandleFunc("POST /schedules/run", s.handleScheduleRun)
	mux.HandleFunc("GET /daily/sources", s.handleSources)
	mux.HandleFunc("POST /daily/sources/config", s.handleSourceConfigAdd)
	mux.HandleFunc("DELETE /daily/sources/config", s.handleSourceConfigDelete)
	mux.HandleFunc("POST /daily/pull", s.handlePull)
	mux.HandleFunc("GET /daily/tickets", s.handleTickets)
	mux.HandleFunc("GET /daily/runs", s.handleRuns)
	// The gallery: what a scheduled run produced, and the bytes of one artifact.
	mux.HandleFunc("GET /daily/artifacts", s.handleDailyArtifacts)
	mux.HandleFunc("GET /daily/artifact", s.handleDailyArtifact)
	mux.HandleFunc("POST /daily/promote", s.handlePromote)
	mux.HandleFunc("GET /knowledge", s.handleKnowledge)
	mux.HandleFunc("POST /knowledge/org", s.handleKnowledgeOrg)
	mux.HandleFunc("DELETE /knowledge/org", s.handleKnowledgeOrgDelete)
	mux.HandleFunc("POST /knowledge/project", s.handleKnowledgeProject)
	mux.HandleFunc("DELETE /knowledge/project", s.handleKnowledgeProjectDelete)
	mux.HandleFunc("POST /knowledge/group", s.handleKnowledgeGroup)
	mux.HandleFunc("DELETE /knowledge/group", s.handleKnowledgeGroupDelete)
	mux.HandleFunc("POST /knowledge/group/links", s.handleKnowledgeGroupLinks)
	mux.HandleFunc("POST /knowledge/relation", s.handleKnowledgeRelation)
	mux.HandleFunc("DELETE /knowledge/relation", s.handleKnowledgeRelationDelete)
	mux.HandleFunc("GET /repos", s.handleRepos)
	mux.HandleFunc("POST /repos", s.handleRepoAdd)
	mux.HandleFunc("DELETE /repos", s.handleRepoDelete)
	mux.HandleFunc("GET /github/app", s.handleGitHubAppStatus)
	mux.HandleFunc("POST /github/app", s.handleGitHubAppSet)
	mux.HandleFunc("DELETE /github/app", s.handleGitHubAppClear)
	mux.HandleFunc("POST /runs", s.handleRunRecord)
	mux.HandleFunc("GET /runs", s.handleRunList)
	mux.HandleFunc("POST /delivery/pull", s.handleDeliveryPull)
	mux.HandleFunc("GET /delivery/issues", s.handleDeliveryIssues)
	mux.HandleFunc("GET /task/meta", s.handleTaskMetaGet)
	mux.HandleFunc("POST /task/meta", s.handleTaskMetaSet)
	return cors(mux)
}

// cors permits the local frontend (Vite dev server and the Tauri webview) to
// call this loopback service from the browser. Loopback-only, so this is safe.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tasksFor collects worktree-backed tasks for a repo (excludes the target branch
// and the main worktree).
func (s *Server) tasksFor(r Repo) ([]Task, error) {
	wts, err := s.g.Worktrees(r.Path)
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, wt := range wts {
		if wt.Bare || wt.Branch == "" || wt.Branch == r.Target || wt.Branch == "(detached)" {
			continue
		}
		stats, err := s.g.NumStat(r.Path, r.Target, wt.Branch)
		if err != nil {
			continue
		}
		add, del := 0, 0
		for _, st := range stats {
			add += st.Additions
			del += st.Deletions
		}
		tasks = append(tasks, Task{
			ID: key(r.Name, wt.Branch), Repo: r.Name, Branch: wt.Branch, Target: r.Target,
			WorktreePath: wt.Path, Additions: add, Deletions: del, Files: len(stats),
			CI: s.ciStatus(r.Name, wt.Branch),
		})
	}
	return tasks, nil
}

func (s *Server) handleTasks(w http.ResponseWriter, _ *http.Request) {
	var all []Task
	for _, r := range s.activeRepos() {
		ts, err := s.tasksFor(r)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		all = append(all, ts...)
	}
	if all == nil {
		all = []Task{}
	}
	writeJSON(w, 200, map[string]any{"tasks": all})
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repo(r.URL.Query().Get("repo"))
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	branch := r.URL.Query().Get("branch")
	file := r.URL.Query().Get("file")
	unified, err := s.g.UnifiedDiff(repo.Path, repo.Target, branch, file)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"files": diff.Parse(unified)})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repo(r.URL.Query().Get("repo"))
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	branch := r.URL.Query().Get("branch")
	path := r.URL.Query().Get("path")
	wt, err := s.worktreePath(repo, branch)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	content, err := s.g.FileContent(wt, path)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": path, "content": content})
}

// handleFiles lists every file in a task's worktree (for the workspace tree).
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repo(r.URL.Query().Get("repo"))
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	wt, err := s.worktreePath(repo, r.URL.Query().Get("branch"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	files, err := s.g.ListFiles(wt)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, 200, map[string]any{"files": files})
}

type fileWriteReq struct {
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// handleFileWrite saves a manual edit into the worktree. Because the deliverable
// changed, the CI gate is reset (self-review must re-run CI before merge).
func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	var req fileWriteReq
	if !decode(w, r, &req) {
		return
	}
	repo, ok := s.repo(req.Repo)
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	wt, err := s.worktreePath(repo, req.Branch)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	if err := s.g.WriteFile(wt, req.Path, req.Content); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.setCI(repo.Name, req.Branch, "none") // edited → CI must re-run
	writeJSON(w, 200, map[string]string{"saved": req.Path})
}

type branchReq struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req branchReq
	if !decode(w, r, &req) {
		return
	}
	repo, ok := s.repo(req.Repo)
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	base := req.Base
	if base == "" {
		base = repo.Target
	}
	dir := filepath.Join(os.TempDir(), "orchestra-wt", repo.Name, sanitize(req.Branch))
	if err := s.g.AddWorktree(repo.Path, dir, req.Branch, base); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]string{"repo": repo.Name, "branch": req.Branch, "worktreePath": dir})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repo(r.URL.Query().Get("repo"))
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	branch := r.URL.Query().Get("branch")
	wt, err := s.worktreePath(repo, branch)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	if err := s.g.RemoveWorktree(repo.Path, wt, true); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"removed": branch})
}

// handleCI runs the repo's configured CI command inside the worktree. The
// self-review gate (merge) requires this to have passed.
func (s *Server) handleCI(w http.ResponseWriter, r *http.Request) {
	var req branchReq
	if !decode(w, r, &req) {
		return
	}
	repo, ok := s.repo(req.Repo)
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	wt, err := s.worktreePath(repo, req.Branch)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	if len(repo.CICommand) == 0 {
		s.setCI(repo.Name, req.Branch, "passed") // no CI configured => open the gate
		writeJSON(w, 200, map[string]string{"status": "passed", "output": "(no ciCommand configured)"})
		return
	}
	s.setCI(repo.Name, req.Branch, "running")
	cmd := exec.Command(repo.CICommand[0], repo.CICommand[1:]...)
	cmd.Dir = wt
	out, runErr := cmd.CombinedOutput()
	status := "passed"
	if runErr != nil {
		status = "failed"
	}
	s.setCI(repo.Name, req.Branch, status)
	writeJSON(w, 200, map[string]string{"status": status, "output": string(out)})
}

func (s *Server) handleMerge(w http.ResponseWriter, r *http.Request) {
	var req branchReq
	if !decode(w, r, &req) {
		return
	}
	repo, ok := s.repo(req.Repo)
	if !ok {
		writeErr(w, 404, "unknown repo")
		return
	}
	if s.ciStatus(repo.Name, req.Branch) != "passed" {
		writeErr(w, 409, "CI must pass before self-review approval")
		return
	}
	out, err := s.g.Merge(repo.Path, repo.Target, req.Branch)
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"merged": req.Branch, "into": repo.Target, "output": out})
}

func (s *Server) worktreePath(repo Repo, branch string) (string, error) {
	wts, err := s.g.Worktrees(repo.Path)
	if err != nil {
		return "", err
	}
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt.Path, nil
		}
	}
	return "", errNotFound
}

var errNotFound = &apiError{"worktree not found for branch"}

type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

func sanitize(s string) string {
	return strings.NewReplacer("/", "-", "..", "-", " ", "-").Replace(s)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, 400, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Run starts the HTTP server (blocking).
func (s *Server) Run() error {
	// Record occurrences the app missed while it was down, then start ticking.
	s.backfillMissed(time.Now())
	s.startScheduler()
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	return srv.ListenAndServe()
}
