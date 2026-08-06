// Package git is a thin, safe wrapper over the git CLI for worktree-based
// review flows. The host agent uses it to materialize each agent's deliverable
// as a git worktree, extract diffs against the target branch, and merge on
// approval — the host side of "sandbox → host review".
package git

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Runner executes git commands against repositories.
type Runner struct {
	bin string
}

func New() *Runner { return &Runner{bin: "git"} }

// noHooks returns the flags that stop host-side git from executing repository
// hooks.
//
// We run git as the host user inside worktrees whose contents an agent wrote.
// Git resolves hooks relative to its git dir, and a worktree's git dir is named
// by the `.git` file *in the worktree* — which the agent can write. Pointing it
// at a directory the agent also controls makes an ordinary `git` call here run
// an agent-authored hook as the host user; that is host code execution, and it
// was reproducible.
//
// The only thing preventing it today is incidental — the agent's write_file
// creates files 0644 and git skips non-executable hooks — which is not a
// control worth depending on. core.hooksPath on the command line beats any
// value in a repo or gitdir config, so this holds wherever the git dir points.
//
// (The sandbox controller carries the same guard for its own git calls; the two
// services are separate modules, so the helper is duplicated rather than shared.)
func noHooks() []string { return []string{"-c", "core.hooksPath=/dev/null"} }

// run executes git in dir and returns stdout, or an error enriched with stderr.
func (g *Runner) run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.bin, append(noHooks(), args...)...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// IsRepo reports whether dir is inside a git working tree, so the UI can reject
// a mistyped repository path up front rather than surfacing it later as an empty
// Delivery board.
func (g *Runner) IsRepo(dir string) bool {
	out, err := g.run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// GitHubSlug returns the "owner/repo" of dir's origin remote, so a per-repo
// GitHub issue pull can target the right repository. Handles both SSH
// (git@github.com:owner/repo.git) and HTTPS (https://github.com/owner/repo.git)
// remotes. Returns an error if there is no origin or it isn't a GitHub remote.
func (g *Runner) GitHubSlug(dir string) (string, error) {
	out, err := g.run(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("no origin remote: %w", err)
	}
	u := strings.TrimSpace(out)
	s := u
	switch {
	case strings.Contains(s, "github.com:"): // SSH: git@github.com:owner/repo(.git)
		s = s[strings.Index(s, "github.com:")+len("github.com:"):]
	case strings.Contains(s, "github.com/"): // HTTPS: https://github.com/owner/repo(.git)
		s = s[strings.Index(s, "github.com/")+len("github.com/"):]
	default:
		return "", fmt.Errorf("origin is not a github.com remote: %s", u)
	}
	s = strings.TrimSuffix(strings.Trim(s, "/"), ".git")
	if parts := strings.Split(s, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot parse owner/repo from origin: %s", u)
	}
	return s, nil
}

// Worktree is one linked working tree of a repository.
type Worktree struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Head   string `json:"head"`
	Bare   bool   `json:"bare"`
}

// Worktrees lists the repository's worktrees (including the main one).
func (g *Runner) Worktrees(repo string) ([]Worktree, error) {
	out, err := g.run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var res []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path != "" {
			res = append(res, cur)
		}
		cur = Worktree{}
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Branch = "(detached)"
		}
	}
	flush()
	return res, nil
}

// AddWorktree creates a worktree at path for branch, based on base. If the
// branch already exists it is checked out; otherwise it is created from base.
func (g *Runner) AddWorktree(repo, path, branch, base string) error {
	if _, err := g.run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err := g.run(repo, "worktree", "add", path, branch)
		return err
	}
	_, err := g.run(repo, "worktree", "add", "-b", branch, path, base)
	return err
}

// RemoveWorktree removes a worktree (force to discard local changes).
func (g *Runner) RemoveWorktree(repo, path string, force bool) error {
	args := []string{"worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	_, err := g.run(repo, args...)
	return err
}

// FileStat is a per-file change summary (git numstat).
type FileStat struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
}

// NumStat returns per-file add/del counts for base...head (merge-base diff).
func (g *Runner) NumStat(repo, base, head string) ([]FileStat, error) {
	out, err := g.run(repo, "diff", "--numstat", base+"..."+head)
	if err != nil {
		return nil, err
	}
	var stats []FileStat
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		fs := FileStat{Path: f[2]}
		if f[0] == "-" || f[1] == "-" {
			fs.Binary = true
		} else {
			fs.Additions, _ = strconv.Atoi(f[0])
			fs.Deletions, _ = strconv.Atoi(f[1])
		}
		stats = append(stats, fs)
	}
	return stats, nil
}

// UnifiedDiff returns the raw unified diff for base...head (optionally one file).
func (g *Runner) UnifiedDiff(repo, base, head, file string) (string, error) {
	args := []string{"diff", "--unified=3", base + "..." + head}
	if file != "" {
		args = append(args, "--", file)
	}
	return g.run(repo, args...)
}

// FileContent reads a file from a worktree directory (the editable "原本").
func (g *Runner) FileContent(worktreeDir, file string) (string, error) {
	abs, err := safeJoin(worktreeDir, file)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WriteFile writes content to a file in the worktree (manual edit of the 原本).
// Parent directories are created; the path must stay inside the worktree.
func (g *Runner) WriteFile(worktreeDir, file, content string) error {
	abs, err := safeJoin(worktreeDir, file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// ListFiles returns every file in the worktree (relative paths), skipping .git.
func (g *Runner) ListFiles(worktreeDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(worktreeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(worktreeDir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

// safeJoin resolves rel against root, rejecting absolute paths and escapes.
func safeJoin(root, rel string) (string, error) {
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid file path")
	}
	return filepath.Join(root, clean), nil
}

// Merge merges branch into target (no-ff) within the repo. Returns combined
// output; on conflict the error carries git's message and the merge is aborted.
func (g *Runner) Merge(repo, target, branch string) (string, error) {
	if _, err := g.run(repo, "checkout", target); err != nil {
		return "", err
	}
	out, err := g.run(repo, "merge", "--no-ff", "-m", fmt.Sprintf("merge %s into %s", branch, target), branch)
	if err != nil {
		g.run(repo, "merge", "--abort")
		return "", err
	}
	return out, nil
}

// Push pushes branch to remoteURL, authenticating with a GitHub App
// installation token.
//
// The token is passed through git's environment-variable config rather than
// embedded in the remote URL or a -c flag, because argv is world-readable via
// ps: anyone on the machine could read a live push credential. Environment is
// only visible to the same user. It also keeps the token out of the error
// message, which quotes the argument list.
//
// The remote is given explicitly instead of relying on a configured origin, so
// this cannot be redirected by whatever the worktree happens to have set.
func (g *Runner) Push(repo, remoteURL, branch, token string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	args := append(noHooks(), "push", remoteURL, "refs/heads/"+branch+":refs/heads/"+branch)
	cmd := exec.CommandContext(ctx, g.bin, args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // fail rather than hang waiting for credentials
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+auth,
	)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git push %s: %v: %s", branch, err, strings.TrimSpace(errb.String()))
	}
	// git writes push progress to stderr; callers want it for the UI.
	return strings.TrimSpace(errb.String() + out.String()), nil
}

// CurrentBranch returns the checked-out branch of a directory.
func (g *Runner) CurrentBranch(dir string) (string, error) {
	out, err := g.run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	return strings.TrimSpace(out), err
}
