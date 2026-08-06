package worktree

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Recorder records stage boundaries as commits in a single shared worktree.
//
// This is the counterpart to Manager. Manager gives each stage its own worktree
// so parallel stages cannot collide, and commits fall out of that naturally.
// Shared mode has no such boundary — every stage edits the same tree — so
// without this, a run leaves one undifferentiated pile of edits and there is no
// way to say what any individual stage produced.
//
// Committing at each boundary makes the worktree's history the record: one
// commit per stage that changed something, each diffable against the stage
// before it. That is what an artifact is.
//
// A Recorder is only correct while stages run one at a time (shared mode's
// existing requirement — concurrent stages would interleave edits into the same
// commit and attribute them to whichever finished first). Calls are serialised
// so a caller cannot corrupt the sequence by racing, but that does not make
// concurrent *stages* meaningful.
type Recorder struct {
	git      string
	dir      string
	identity []string

	mu sync.Mutex
}

// FileChange is one path touched by a stage, with its line counts. Additions and
// Deletions are -1 for binary files, which git reports as "-".
type FileChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Snapshot is what one stage produced. Commit is empty when the stage changed
// nothing — a real outcome (a planner that only reads, say), not a failure.
type Snapshot struct {
	Commit string       `json:"commit"`
	Parent string       `json:"parent"`
	Files  []FileChange `json:"files"`
}

// Empty reports whether the stage left the tree untouched.
func (s Snapshot) Empty() bool { return s.Commit == "" }

// NewRecorder returns a Recorder for worktreePath, which must be a git worktree.
//
// Any changes already sitting in the worktree are committed first, as a baseline
// attributed to the run rather than to a stage. Without this the first stage's
// `git add -A` sweeps up whatever the tree happened to be carrying — leftovers
// from an earlier run, a half-finished edit — and reports it as that stage's
// output. Observed in practice: a planner that only read files was credited with
// 3 files and 111 added lines from a previous run.
func NewRecorder(worktreePath, runID string) (*Recorder, error) {
	r := &Recorder{
		git:      "git",
		dir:      worktreePath,
		identity: []string{"-c", "user.name=orchestra", "-c", "user.email=orchestra@local"},
	}
	if _, err := r.run("rev-parse", "HEAD"); err != nil {
		return nil, fmt.Errorf("not a git worktree: %w", err)
	}
	if err := r.commitBaseline(runID); err != nil {
		return nil, err
	}
	return r, nil
}

// commitBaseline absorbs any pre-existing changes so stage commits contain only
// what their stage did. A clean worktree commits nothing.
func (r *Recorder) commitBaseline(runID string) error {
	if _, err := r.run("add", "-A"); err != nil {
		return err
	}
	if _, err := r.run("diff", "--cached", "--quiet"); err == nil {
		return nil // already clean
	}
	args := append(append([]string{}, r.identity...), "commit", "-m", "orchestra run "+runID+" baseline")
	_, err := r.run(args...)
	return err
}

// Commit stages everything in the worktree and commits it as stageID's result,
// returning what changed. When nothing changed it commits nothing and returns an
// empty Snapshot.
func (r *Recorder) Commit(stageID string) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	parent, err := r.head()
	if err != nil {
		return Snapshot{}, err
	}
	if _, err := r.run("add", "-A"); err != nil {
		return Snapshot{}, err
	}
	// `diff --cached --quiet` exits zero when nothing is staged.
	if _, err := r.run("diff", "--cached", "--quiet"); err == nil {
		return Snapshot{}, nil
	}
	args := append(append([]string{}, r.identity...), "commit", "-m", "orchestra stage "+stageID)
	if _, err := r.run(args...); err != nil {
		return Snapshot{}, err
	}
	sha, err := r.head()
	if err != nil {
		return Snapshot{}, err
	}
	files, err := r.numstat(sha)
	if err != nil {
		// The commit landed; losing the per-file breakdown should not fail the
		// stage, so report the boundary without it.
		return Snapshot{Commit: sha, Parent: parent}, nil
	}
	return Snapshot{Commit: sha, Parent: parent, Files: files}, nil
}

// numstat lists the paths a commit touched with their line counts.
func (r *Recorder) numstat(sha string) ([]FileChange, error) {
	out, err := r.run("show", "--numstat", "--format=", sha)
	if err != nil {
		return nil, err
	}
	files := []FileChange{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 3 || fields[2] == "" {
			continue
		}
		files = append(files, FileChange{
			Path:      fields[2],
			Additions: countOrBinary(fields[0]),
			Deletions: countOrBinary(fields[1]),
		})
	}
	return files, nil
}

// countOrBinary parses a numstat count; git writes "-" for binary files.
func countOrBinary(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

func (r *Recorder) head() (string, error) {
	out, err := r.run("rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// run executes git in the shared worktree. noHooks applies here for the same
// reason it does everywhere else: the tree holds agent-authored content.
func (r *Recorder) run(args ...string) (string, error) {
	return runGit(r.git, r.dir, args...)
}
