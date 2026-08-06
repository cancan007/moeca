package docker

import (
	"os"
	"strings"
	"testing"
)

// argIndex returns the position of the first exact match of want in args, or -1.
func argIndex(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// hasFlagValue reports whether args contains flag immediately followed by value.
func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestRunArgs_HardeningFlags(t *testing.T) {
	spec := Spec{
		TaskID:       "web-app-feature-x",
		WorktreePath: "/tmp/orchestra-wt/web-app/feature-x",
		Image:        "orchestra/agent:1.2",
		Cmd:          []string{"claude", "--task", "feature-x"},
		Env:          map[string]string{"ANTHROPIC_BASE_URL": "http://host.docker.internal:8787/anthropic"},
	}
	args := RunArgs(spec)

	// starts with `run -d`
	if len(args) < 2 || args[0] != "run" || args[1] != "-d" {
		t.Fatalf("expected args to start with [run -d], got %v", args[:min(2, len(args))])
	}

	// labels
	if !hasFlagValue(args, "--label", "orchestra=1") {
		t.Errorf("missing --label orchestra=1: %v", args)
	}
	if !hasFlagValue(args, "--label", "orchestra.task=web-app-feature-x") {
		t.Errorf("missing --label orchestra.task=<taskId>: %v", args)
	}

	// name
	if !hasFlagValue(args, "--name", "orchestra-web-app-feature-x") {
		t.Errorf("missing --name orchestra-<taskId>: %v", args)
	}

	// ONLY the worktree is mounted, read-write, at /work
	if !hasFlagValue(args, "-v", "/tmp/orchestra-wt/web-app/feature-x:/work:rw") {
		t.Errorf("missing worktree mount: %v", args)
	}
	if !hasFlagValue(args, "-w", "/work") {
		t.Errorf("missing workdir /work: %v", args)
	}
	// there must be exactly one -v (nothing else mounted)
	mounts := 0
	for _, a := range args {
		if a == "-v" {
			mounts++
		}
	}
	if mounts != 1 {
		t.Errorf("expected exactly one -v mount, got %d: %v", mounts, args)
	}

	// read-only root fs + tmpfs
	if argIndex(args, "--read-only") < 0 {
		t.Errorf("missing --read-only: %v", args)
	}
	if !hasFlagValue(args, "--tmpfs", "/tmp") {
		t.Errorf("missing --tmpfs /tmp: %v", args)
	}

	// dedicated network
	if !hasFlagValue(args, "--network", DefaultNetwork) {
		t.Errorf("missing --network %s: %v", DefaultNetwork, args)
	}

	// dropped privileges
	if !hasFlagValue(args, "--cap-drop", "ALL") {
		t.Errorf("missing --cap-drop ALL: %v", args)
	}
	if !hasFlagValue(args, "--security-opt", "no-new-privileges") {
		t.Errorf("missing --security-opt no-new-privileges: %v", args)
	}

	// resource caps
	if !hasFlagValue(args, "--pids-limit", "512") {
		t.Errorf("missing default --pids-limit 512: %v", args)
	}
	if !hasFlagValue(args, "--memory", "2048m") {
		t.Errorf("missing default --memory 2048m: %v", args)
	}
	if !hasFlagValue(args, "--cpus", "2") {
		t.Errorf("missing default --cpus 2: %v", args)
	}

	// allowed env passed through
	if !hasFlagValue(args, "--env", "ANTHROPIC_BASE_URL=http://host.docker.internal:8787/anthropic") {
		t.Errorf("missing allowed --env: %v", args)
	}

	// image precedes command; command is last
	imgIdx := argIndex(args, "orchestra/agent:1.2")
	if imgIdx < 0 {
		t.Fatalf("image not found in args: %v", args)
	}
	tail := args[imgIdx+1:]
	if strings.Join(tail, " ") != "claude --task feature-x" {
		t.Errorf("expected command after image to be [claude --task feature-x], got %v", tail)
	}
}

func TestRunArgs_DoesNotLeakHostEnv(t *testing.T) {
	// Set a secret-looking var in the host environment; it must NOT appear.
	const secret = "ORCHESTRA_TEST_SECRET_TOKEN"
	os.Setenv(secret, "super-secret-value")
	defer os.Unsetenv(secret)

	spec := Spec{
		TaskID:       "t1",
		WorktreePath: "/work/t1",
		Env:          map[string]string{"ALLOWED": "yes"},
	}
	args := RunArgs(spec)
	joined := strings.Join(args, " ")

	if strings.Contains(joined, secret) || strings.Contains(joined, "super-secret-value") {
		t.Fatalf("host environment leaked into docker args: %v", args)
	}
	if !hasFlagValue(args, "--env", "ALLOWED=yes") {
		t.Errorf("allowed env var not passed: %v", args)
	}
	// exactly one --env (the allowed one), proving no host passthrough
	envCount := 0
	for _, a := range args {
		if a == "--env" {
			envCount++
		}
	}
	if envCount != 1 {
		t.Errorf("expected exactly one --env, got %d: %v", envCount, args)
	}
}

func TestRunArgs_Defaults(t *testing.T) {
	// A minimal spec must acquire safe defaults.
	args := RunArgs(Spec{TaskID: "t2", WorktreePath: "/work/t2"})

	if !hasFlagValue(args, "--network", DefaultNetwork) {
		t.Errorf("default network not applied: %v", args)
	}
	if !hasFlagValue(args, "--memory", "2048m") {
		t.Errorf("default memory not applied: %v", args)
	}
	if !hasFlagValue(args, "--cpus", "2") {
		t.Errorf("default cpus not applied: %v", args)
	}
	if !hasFlagValue(args, "--pids-limit", "512") {
		t.Errorf("default pids-limit not applied: %v", args)
	}
	// default image is the last arg (no command supplied)
	if args[len(args)-1] != DefaultImage {
		t.Errorf("expected default image as trailing arg, got %q: %v", args[len(args)-1], args)
	}
}

func TestRunArgs_CustomLimits(t *testing.T) {
	args := RunArgs(Spec{
		TaskID:       "t3",
		WorktreePath: "/work/t3",
		Network:      "custom-net",
		MemoryMB:     512,
		CPUs:         0.5,
		PidsLimit:    128,
	})
	if !hasFlagValue(args, "--network", "custom-net") {
		t.Errorf("custom network not applied: %v", args)
	}
	if !hasFlagValue(args, "--memory", "512m") {
		t.Errorf("custom memory not applied: %v", args)
	}
	if !hasFlagValue(args, "--cpus", "0.5") {
		t.Errorf("custom cpus not applied: %v", args)
	}
	if !hasFlagValue(args, "--pids-limit", "128") {
		t.Errorf("custom pids-limit not applied: %v", args)
	}
}

// A real toolchain cannot run on a read-only root: npm wants ~/.npm, pip wants
// ~/.cache, go wants GOMODCACHE. Each language image declares the scratch paths
// it needs and they arrive as tmpfs — RAM-backed and gone with the container, so
// nothing persists across tasks.
func TestRunArgs_ExtraTmpfsForToolchainCaches(t *testing.T) {
	args := RunArgs(Spec{
		TaskID:       "t4",
		WorktreePath: "/work/t4",
		Tmpfs:        []string{"/home/agent/.npm", "/go/pkg/mod", "/home/agent/.cache"},
	})
	for _, p := range []string{"/tmp", "/home/agent/.npm", "/go/pkg/mod", "/home/agent/.cache"} {
		if !hasFlagValue(args, "--tmpfs", p) {
			t.Errorf("missing --tmpfs %s: %v", p, args)
		}
	}
	// Sorted, so the same spec always produces the same argv.
	var got []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--tmpfs" && args[i+1] != "/tmp" {
			got = append(got, args[i+1])
		}
	}
	want := "/go/pkg/mod /home/agent/.cache /home/agent/.npm"
	if strings.Join(got, " ") != want {
		t.Errorf("tmpfs order = %q, want %q", strings.Join(got, " "), want)
	}
}

// /tmp is always mounted; listing it again would hand docker the same
// mountpoint twice.
func TestRunArgs_TmpfsDedupes(t *testing.T) {
	args := RunArgs(Spec{
		TaskID:       "t5",
		WorktreePath: "/work/t5",
		Tmpfs:        []string{"/tmp", "/cache", "/cache", "  "},
	})
	tmpfsCount := 0
	for _, a := range args {
		if a == "--tmpfs" {
			tmpfsCount++
		}
	}
	if tmpfsCount != 2 { // /tmp + /cache
		t.Errorf("expected 2 --tmpfs flags (/tmp, /cache), got %d: %v", tmpfsCount, args)
	}
}

// The hardening surface is code and the image is data. Whatever image a stage
// selects — a shipped one, a user's custom one, a resolved digest — every
// isolation flag must still be there. This is the invariant that makes the
// image allowlist a supply-chain control rather than a security boundary.
func TestRunArgs_HardeningIsIndependentOfTheImage(t *testing.T) {
	images := []string{
		DefaultImage,
		"orchestra/agent-media:latest",
		"sha256:2f1c9e5b6a4d8f0e3b7c1a5d9e2f4b6c8a0d3e5f7b9c1a3d5e7f9b1c3d5e7f9b",
		"ghcr.io/someone/untrusted@sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}
	for _, img := range images {
		args := RunArgs(Spec{TaskID: "t6", WorktreePath: "/work/t6", Image: img, Network: "none"})
		for _, flag := range []string{"--read-only", "--security-opt", "--cap-drop", "--pids-limit", "--memory", "--cpus"} {
			if argIndex(args, flag) < 0 {
				t.Errorf("image %q: missing %s: %v", img, flag, args)
			}
		}
		if !hasFlagValue(args, "--cap-drop", "ALL") {
			t.Errorf("image %q: --cap-drop is not ALL: %v", img, args)
		}
		if !hasFlagValue(args, "-v", "/work/t6:/work:rw") {
			t.Errorf("image %q: worktree mount changed: %v", img, args)
		}
	}
}

// A media stage transcodes untrusted input and has no reason to talk to
// anything — not even the gateway. `--network none` is a posture the policy
// table can select, and it must reach docker verbatim.
func TestRunArgs_NetworkNone(t *testing.T) {
	args := RunArgs(Spec{TaskID: "t7", WorktreePath: "/work/t7", Network: "none"})
	if !hasFlagValue(args, "--network", "none") {
		t.Errorf("missing --network none: %v", args)
	}
}

func TestParsePS(t *testing.T) {
	out := "abc123\torchestra-t1\torchestra/agent:1.2\tUp 3 minutes\tt1\n" +
		"def456\torchestra-t2\torchestra/agent:1.2\tExited (0) 1 minute ago\tt2"
	got := parsePS(out)
	if len(got) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(got))
	}
	if got[0].ID != "abc123" || got[0].Name != "orchestra-t1" || got[0].TaskID != "t1" {
		t.Errorf("unexpected first container: %+v", got[0])
	}
	if got[1].Status != "Exited (0) 1 minute ago" {
		t.Errorf("unexpected status: %q", got[1].Status)
	}
}
