// Package worktree gives the orchestrator per-stage ISOLATED git worktrees so
// parallel stages of a run never clobber one another's files. Each stage runs on
// its own branch, seeded from the merge of its dependencies' outputs; on
// completion the stage's changes are committed to that branch, and the run's
// sink stages are merged back into the base worktree the review flow inspects.
//
// The manager runs the host git CLI directly — the sandbox controller is a
// host-side process (not itself sandboxed), and the base worktree it is handed
// is a real git worktree of the repository. This is orthogonal to the network
// isolation of the agent containers.
package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Manager owns the per-stage worktrees for a single run.
type Manager struct {
	git        string
	base       string   // base worktree directory (a git worktree)
	baseCommit string   // commit every stage branch derives from
	root       string   // parent dir holding the per-stage worktrees
	runID      string
	identity   []string // -c user.name/email so commits always have an author

	mu     sync.Mutex
	stages map[string]stageInfo // stageID -> its worktree/branch
}

type stageInfo struct {
	dir    string
	branch string
}

// New prepares a manager for baseWorktree, which must be a git worktree. It
// records the base commit all stage branches will fork from and creates a
// scratch directory to hold the per-stage worktrees.
func New(baseWorktree, runID string) (*Manager, error) {
	m := &Manager{
		git:      "git",
		base:     baseWorktree,
		runID:    runID,
		identity: []string{"-c", "user.name=orchestra", "-c", "user.email=orchestra@local"},
		stages:   map[string]stageInfo{},
	}
	head, err := m.run(baseWorktree, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("base is not a git worktree: %w", err)
	}
	m.baseCommit = strings.TrimSpace(head)
	m.root = filepath.Join(os.TempDir(), "orchestra-run", sanitize(runID))
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return nil, err
	}
	return m, nil
}

// Prepare creates the isolated worktree for a stage. parents are the result
// commits of the stage's dependencies (as returned by Commit); the worktree is
// seeded from the first parent and the rest are merged in (base commit when the
// stage has no dependencies). A merge conflict is returned as an error and the
// half-made worktree is removed.
func (m *Manager) Prepare(stageID string, parents []string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	branch := m.branchName(stageID)
	dir := filepath.Join(m.root, sanitize(stageID))
	seed := m.baseCommit
	if len(parents) > 0 {
		seed = parents[0]
	}
	if _, err := m.run(m.base, "worktree", "add", "-b", branch, dir, seed); err != nil {
		return "", fmt.Errorf("create worktree for stage %q: %w", stageID, err)
	}
	m.stages[stageID] = stageInfo{dir: dir, branch: branch}

	var rest []string
	if len(parents) > 1 {
		rest = parents[1:]
	}
	for _, p := range rest {
		if _, err := m.run(dir, append(m.identity, "merge", "--no-edit", p)...); err != nil {
			m.run(dir, "merge", "--abort")
			m.run(m.base, "worktree", "remove", "--force", dir)
			delete(m.stages, stageID)
			return "", fmt.Errorf("merge dependency into stage %q (conflict?): %w", stageID, err)
		}
	}
	return dir, nil
}

// Commit records the stage's working-tree changes as a commit on its branch and
// returns the resulting commit sha. When the stage changed nothing, the current
// HEAD (its seed) is returned so dependents still have a valid parent commit.
func (m *Manager) Commit(stageID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.stages[stageID]
	if !ok {
		return "", fmt.Errorf("stage %q has no worktree", stageID)
	}
	if _, err := m.run(info.dir, "add", "-A"); err != nil {
		return "", err
	}
	// `diff --cached --quiet` exits non-zero when something is staged.
	if _, err := m.run(info.dir, "diff", "--cached", "--quiet"); err == nil {
		return m.head(info.dir) // nothing to commit
	}
	if _, err := m.run(info.dir, append(m.identity, "commit", "-m", "orchestra stage "+stageID)...); err != nil {
		return "", err
	}
	return m.head(info.dir)
}

// Integrate merges the given commits into the base worktree's branch, landing
// the run's output where the review/merge flow will find it. A conflict aborts
// that merge and returns an error.
func (m *Manager) Integrate(commits []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range commits {
		if c == "" || c == m.baseCommit {
			continue
		}
		if _, err := m.run(m.base, append(m.identity, "merge", "--no-edit", c)...); err != nil {
			m.run(m.base, "merge", "--abort")
			return fmt.Errorf("integrate %s into base (conflict?): %w", c[:min(7, len(c))], err)
		}
	}
	return nil
}

// StageDir returns the worktree directory prepared for a stage (empty if none).
func (m *Manager) StageDir(stageID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stages[stageID].dir
}

// Cleanup removes every per-stage worktree and branch and the scratch root. It
// is best-effort: errors are ignored so a failed run still tidies up.
func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, info := range m.stages {
		m.run(m.base, "worktree", "remove", "--force", info.dir)
		m.run(m.base, "branch", "-D", info.branch)
	}
	os.RemoveAll(m.root)
	m.stages = map[string]stageInfo{}
}

func (m *Manager) branchName(stageID string) string {
	return "orchestra/" + sanitize(m.runID) + "/" + sanitize(stageID)
}

func (m *Manager) head(dir string) (string, error) {
	out, err := m.run(dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// noHooks returns the flags that stop host-side git from executing repository
// hooks.
//
// These worktrees hold agent-authored content, and this process runs git inside
// them as the host user. Git resolves hooks relative to its git dir, and a
// worktree's git dir is named by the `.git` file *in the worktree* — which the
// agent can write. Pointing it at a directory the agent also controls makes
// `git commit` run an agent-authored hook as the host user; that is host code
// execution, and it was reproducible.
//
// The only thing preventing it today is incidental: the agent's write_file
// creates files 0644, and git skips non-executable hooks. That is not a control
// we should depend on — anything that later yields an executable bit (a shell
// tool, an archive extractor, editing a file that is already executable, or a
// repo shipping its own hooks with core.hooksPath) re-opens it.
//
// core.hooksPath on the command line beats any value in a repo or gitdir
// config, so this holds regardless of where the git dir points.
func noHooks() []string { return []string{"-c", "core.hooksPath=/dev/null"} }

// run executes git in dir and returns stdout, or an error enriched with stderr.
// Callers that need commit identity pass the leading `-c key=val` flags (git
// requires them before the subcommand) via m.identity.
//
// noHooks is prepended to every invocation — see its doc comment. It must stay
// ahead of the subcommand, which is also where m.identity sits, so both are
// simply concatenated in front of args.
func (m *Manager) run(dir string, args ...string) (string, error) {
	return runGit(m.git, dir, args...)
}

// runGit executes git in dir and returns stdout, or an error enriched with
// stderr. Every git call in this package goes through here so the hook
// neutralisation cannot be forgotten on a new code path.
func runGit(bin, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(noHooks(), args...)...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// sanitize reduces an id to characters safe in a filesystem path and a git ref.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		return "x"
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
