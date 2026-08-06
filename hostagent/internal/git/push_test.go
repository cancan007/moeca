package git

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGit stands in for the git binary, recording the argv and environment it
// was invoked with.
func fakeGit(t *testing.T) (r *Runner, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	dir = t.TempDir()
	bin := filepath.Join(dir, "git")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > " + filepath.Join(dir, "argv.txt") + "\n" +
		"env > " + filepath.Join(dir, "env.txt") + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{bin: bin}, dir
}

const secret = "ghs_supersecrettoken"

// argv is world-readable through ps, so a push credential must never appear
// there — anyone on the machine could otherwise read a live token.
func TestPushKeepsTokenOutOfArgv(t *testing.T) {
	r, dir := fakeGit(t)

	if _, err := r.Push(dir, "https://github.com/o/r.git", "feat/x", secret); err != nil {
		t.Fatalf("Push: %v", err)
	}

	argv, err := os.ReadFile(filepath.Join(dir, "argv.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argv), secret) {
		t.Errorf("token leaked into argv: %s", argv)
	}
	// The remote is given explicitly rather than relying on a configured origin.
	if !strings.Contains(string(argv), "https://github.com/o/r.git") {
		t.Errorf("argv = %s, want the explicit remote", argv)
	}
	if !strings.Contains(string(argv), "refs/heads/feat/x:refs/heads/feat/x") {
		t.Errorf("argv = %s, want an explicit refspec", argv)
	}
}

// The credential travels in the environment instead, which only the same user
// can read.
func TestPushPassesTokenThroughEnvironment(t *testing.T) {
	r, dir := fakeGit(t)

	if _, err := r.Push(dir, "https://github.com/o/r.git", "feat/x", secret); err != nil {
		t.Fatalf("Push: %v", err)
	}

	env, err := os.ReadFile(filepath.Join(dir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(env)
	// base64("x-access-token:" + secret)
	if !strings.Contains(got, "GIT_CONFIG_KEY_0=http.extraHeader") {
		t.Error("auth header config not passed via environment")
	}
	if !strings.Contains(got, "Authorization: Basic ") {
		t.Error("no Authorization header in the environment config")
	}
	// A hung push waiting on a credential prompt would look like a stall.
	if !strings.Contains(got, "GIT_TERMINAL_PROMPT=0") {
		t.Error("terminal prompt not disabled")
	}
}

// The hook guard applies here too: the branch being pushed holds agent-authored
// content, and a pre-push hook would run as the host user.
func TestPushDisablesHooks(t *testing.T) {
	r, dir := fakeGit(t)

	if _, err := r.Push(dir, "https://github.com/o/r.git", "feat/x", secret); err != nil {
		t.Fatalf("Push: %v", err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv.txt"))
	if !strings.Contains(string(argv), "core.hooksPath=/dev/null") {
		t.Errorf("argv = %s, want hooks neutralised", argv)
	}
}
