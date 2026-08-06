package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

// Writing the worktree's .git repoints the git dir that host-side git resolves
// hooks from — the escalation path that made an agent-authored hook run as the
// host user. It is refused structurally, not by configurable policy.
func TestGitMetadataIsNeverWritable(t *testing.T) {
	r := New(t.TempDir())
	r.SetDenyPaths(nil) // policy deliberately empty

	for _, p := range []string{
		".git",
		".git/config",
		".git/hooks/pre-commit",
		"./.git",
		".git/../.git/hooks/pre-commit",
	} {
		if out, isErr := r.Dispatch("write_file", map[string]any{"path": p, "content": "x"}); !isErr {
			t.Errorf("write_file(%q) was allowed: %s", p, out)
		}
		if _, err := r.resolve(p); err == nil {
			t.Errorf("resolve(%q) allowed git metadata", p)
		}
	}
}

// A nested repository's metadata is a different thing from this worktree's, and
// ordinary files whose names merely start with .git must stay writable.
func TestOnlyTheWorktreeGitDirIsRefused(t *testing.T) {
	root := t.TempDir()
	r := New(root)

	for _, p := range []string{".gitignore", ".github/workflows/ci.yml", "docs/git.md"} {
		if _, err := r.resolve(p); err != nil {
			t.Errorf("resolve(%q) should be allowed: %v", p, err)
		}
	}
	abs, err := r.resolve(".gitignore")
	if err != nil || !strings.HasPrefix(abs, filepath.Clean(root)) {
		t.Errorf("resolve(.gitignore) = %q, %v", abs, err)
	}
}
