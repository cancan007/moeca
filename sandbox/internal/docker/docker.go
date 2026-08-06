// Package docker is a thin, safe wrapper over the `docker` CLI used to run
// Orchestra agents inside hardened, credential-free sandboxes.
//
// The isolation contract: an agent container can read and write ONLY its task's
// git worktree (mounted at /work), has a read-only root filesystem, drops every
// Linux capability, cannot gain new privileges, and is attached to a dedicated
// egress network. It carries no host environment and no secrets — every outbound
// API call goes through the separate Orchestra gateway, which injects keys. The
// host process environment is NEVER passed through to a container.
//
// RunArgs builds the `docker run` argument vector as a pure function so the whole
// hardening surface can be unit-tested without a running Docker daemon.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Default sandbox limits. Applied by (Spec).normalize when a field is zero.
const (
	DefaultNetwork   = "orchestra-egress" // dedicated non-default egress network
	DefaultImage     = "orchestra/agent:latest"
	DefaultMemoryMB  = 2048
	DefaultCPUs      = 2.0
	DefaultPidsLimit = 512
)

// Spec describes one agent sandbox to launch.
type Spec struct {
	// TaskID is the Orchestra task this sandbox runs; used for labels and name.
	TaskID string `json:"taskId"`
	// WorktreePath is the host path to the task's git worktree. It is the ONLY
	// host path mounted into the container (at /work, read-write).
	WorktreePath string `json:"worktreePath"`
	// Image is the container image to run.
	Image string `json:"image"`
	// Cmd is the command (and args) to exec in the container. Empty => image default.
	Cmd []string `json:"cmd"`
	// Env holds ONLY explicitly-allowed, non-secret variables (e.g. gateway base
	// URLs). Never populate this from the host environment.
	Env map[string]string `json:"env"`
	// Network is the docker network to attach (default DefaultNetwork).
	Network string `json:"network"`
	// MemoryMB caps container memory (default DefaultMemoryMB).
	MemoryMB int `json:"memoryMB"`
	// CPUs caps CPU share (default DefaultCPUs).
	CPUs float64 `json:"cpus"`
	// PidsLimit caps the number of processes (default DefaultPidsLimit).
	PidsLimit int `json:"pidsLimit"`
	// Tmpfs are extra writable scratch paths, mounted as tmpfs on top of the
	// read-only root (/tmp is always mounted and need not be listed).
	//
	// A read-only root filesystem and a real toolchain collide: npm wants
	// ~/.npm, pip wants ~/.cache, go wants GOMODCACHE. Each language image
	// therefore declares the paths it needs. They are RAM-backed and die with
	// the container, which is the point — the sandbox stays disposable, and the
	// durable dependency cache lives in the registry proxy instead of in a
	// volume shared between tasks.
	Tmpfs []string `json:"tmpfs"`
	// Name overrides the container name. Empty => ContainerName(TaskID).
	//
	// The orchestrator sets this to a run-scoped name so two runs of the same
	// task+stage don't collide: the name is what docker enforces uniqueness on,
	// and a finished run's containers are deliberately kept for log retrieval.
	// TaskID stays run-independent so the orchestra.task label and List() keep
	// grouping by task.
	Name string `json:"name"`
}

// ContainerName is the name docker will be asked to use for this spec.
func (s Spec) ContainerName() string {
	if s.Name != "" {
		return s.Name
	}
	return ContainerName(s.TaskID)
}

// normalize fills zero-valued fields with sane, safe defaults and returns a copy.
func (s Spec) normalize() Spec {
	if s.Image == "" {
		s.Image = DefaultImage
	}
	if s.Network == "" {
		s.Network = DefaultNetwork
	}
	if s.MemoryMB <= 0 {
		s.MemoryMB = DefaultMemoryMB
	}
	if s.CPUs <= 0 {
		s.CPUs = DefaultCPUs
	}
	if s.PidsLimit <= 0 {
		s.PidsLimit = DefaultPidsLimit
	}
	return s
}

// ContainerName returns the deterministic container name for a task.
func ContainerName(taskID string) string { return "orchestra-" + taskID }

// RunArgs builds the full `docker run` argument vector for a spec, including
// every isolation flag. It is pure (no daemon, no env access) so the hardening
// surface can be asserted in unit tests.
//
// The container is started detached (-d). Ordering: flags, then the image, then
// the command — exactly as `docker run [OPTIONS] IMAGE [COMMAND]` requires.
func RunArgs(spec Spec) []string {
	s := spec.normalize()
	args := []string{
		"run", "-d",
		// identify Orchestra-managed containers and their task
		"--label", "orchestra=1",
		"--label", "orchestra.task=" + s.TaskID,
		"--name", s.ContainerName(),
		// ONLY the task's worktree is mounted; nothing else from the host
		"-v", s.WorktreePath + ":/work:rw",
		"-w", "/work",
		// immutable root fs with a writable scratch tmpfs
		"--read-only",
		"--tmpfs", "/tmp",
	}

	// Extra writable scratch for toolchain caches, sorted for determinism and
	// deduped against the always-present /tmp.
	for _, p := range dedupedTmpfs(s.Tmpfs) {
		args = append(args, "--tmpfs", p)
	}

	args = append(args,
		// dedicated egress network (not the default bridge)
		"--network", s.Network,
		// drop all privileges
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		// resource caps
		"--pids-limit", strconv.Itoa(s.PidsLimit),
		"--memory", strconv.Itoa(s.MemoryMB) + "m",
		"--cpus", strconv.FormatFloat(s.CPUs, 'f', -1, 64),
	)

	// Only explicitly-allowed, non-secret env vars. Sorted for determinism.
	// The host process environment is NEVER read here.
	for _, k := range sortedKeys(s.Env) {
		args = append(args, "--env", k+"="+s.Env[k])
	}

	// image, then command
	args = append(args, s.Image)
	args = append(args, s.Cmd...)
	return args
}

// dedupedTmpfs returns the extra tmpfs paths, sorted and with duplicates (and
// the always-mounted /tmp) removed, so RunArgs stays deterministic and docker
// is never handed the same mountpoint twice.
func dedupedTmpfs(paths []string) []string {
	seen := map[string]bool{"/tmp": true}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Container is one Orchestra-managed sandbox as reported by `docker ps`.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
	TaskID string `json:"taskId"`
}

// Runner executes the docker CLI. It is the exec-wrapper layer; the API server
// depends on the narrower Client interface so it can be stubbed in tests.
type Runner struct {
	bin     string
	timeout time.Duration
}

// New returns a Runner backed by the `docker` binary on PATH.
func New() *Runner { return &Runner{bin: "docker", timeout: 60 * time.Second} }

// run executes docker and returns trimmed stdout, or an error enriched with stderr.
func (r *Runner) run(args ...string) (string, error) {
	return r.runWithTimeout(r.timeout, args...)
}

// runWithTimeout is run with an explicit timeout (used by Wait, which must block
// for the full duration of an agent run). It reads no shared mutable state, so
// it is safe to call concurrently.
func (r *Runner) runWithTimeout(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// EnsureNetwork makes sure a docker network exists, creating it if absent. When
// internal is true the network is created with --internal: containers on it have
// NO route to the host or the internet, so a sandbox can reach only other
// members of the network (i.e. the gateway). Idempotent: an already-existing
// network is left as-is.
func (r *Runner) EnsureNetwork(name string, internal bool) error {
	if name == "" {
		return fmt.Errorf("docker: EnsureNetwork requires a name")
	}
	if _, err := r.run("network", "inspect", name); err == nil {
		return nil // already exists
	}
	args := []string{"network", "create"}
	if internal {
		args = append(args, "--internal")
	}
	args = append(args, name)
	_, err := r.run(args...)
	return err
}

// Running reports whether a container exists and is currently running. A
// missing container is (false, false) with no error — `docker inspect` exits
// non-zero for an unknown name, which is not a failure here.
func (r *Runner) Running(name string) (exists, running bool) {
	out, err := r.run("inspect", "-f", "{{.State.Running}}", name)
	if err != nil {
		return false, false
	}
	return true, strings.TrimSpace(out) == "true"
}

// Create launches a hardened sandbox container (detached) and returns its id.
//
// Docker enforces uniqueness on the container name, and a finished run's
// containers are deliberately kept so their logs can still be read. Names are
// therefore run-scoped (see Spec.Name) and normally never collide. When one
// does — a retry inside the same run, or a caller that did not scope its name —
// clear it only if it has already exited. Force-removing a *running* container
// would silently kill a live agent, and removing an exited one discards its
// logs, so callers that need those must persist them before re-creating.
func (r *Runner) Create(spec Spec) (string, error) {
	if spec.TaskID == "" {
		return "", fmt.Errorf("docker: create requires a taskId")
	}
	if spec.WorktreePath == "" {
		return "", fmt.Errorf("docker: create requires a worktreePath")
	}
	name := spec.normalize().ContainerName()
	if exists, running := r.Running(name); exists {
		if running {
			return "", fmt.Errorf("docker: container %q is already running", name)
		}
		if err := r.Remove(name); err != nil {
			return "", fmt.Errorf("docker: clearing exited container %q: %w", name, err)
		}
	}
	return r.run(RunArgs(spec)...)
}

// pullTimeout bounds an image pull, which is far slower than any other docker
// call this wrapper makes.
const pullTimeout = 15 * time.Minute

// Resolve turns an image reference into the immutable image ID that reference
// currently points at, pulling the image first if it is not present locally.
//
// Tags move. A run recorded as `orchestra/agent:latest` says nothing about which
// bytes actually executed, and the same run replayed a week later can be a
// different image entirely. Resolving host-side to the content digest and
// launching THAT is what makes a run reproducible and its audit line meaningful.
//
// Pulling here also keeps image fetching a host action: the sandbox never
// triggers a registry fetch of its own, and a pull failure surfaces as a clear
// controller error instead of a container that mysteriously will not start.
func (r *Runner) Resolve(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("docker: resolve requires an image reference")
	}
	if id, err := r.imageID(ref); err == nil && id != "" {
		return id, nil
	}
	if _, err := r.runWithTimeout(pullTimeout, "pull", ref); err != nil {
		return "", fmt.Errorf("docker: pulling %s: %w", ref, err)
	}
	id, err := r.imageID(ref)
	if err != nil {
		return "", fmt.Errorf("docker: inspecting %s after pull: %w", ref, err)
	}
	if id == "" {
		return "", fmt.Errorf("docker: %s resolved to an empty image id", ref)
	}
	return id, nil
}

// imageID reports the local content digest of an image reference.
func (r *Runner) imageID(ref string) (string, error) {
	out, err := r.run("image", "inspect", "-f", "{{.Id}}", ref)
	return strings.TrimSpace(out), err
}

// Logs returns the combined stdout/stderr log of a container.
func (r *Runner) Logs(id string) (string, error) {
	return r.run("logs", id)
}

// Wait blocks until the container exits and returns its exit code. It uses a
// long timeout since an agent run can be lengthy.
func (r *Runner) Wait(id string) (int, error) {
	out, err := r.runWithTimeout(24*time.Hour, "wait", id)
	if err != nil {
		return -1, err
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		return -1, fmt.Errorf("docker wait %s: unexpected output %q", id, out)
	}
	return code, nil
}

// Stop stops a running container.
func (r *Runner) Stop(id string) error {
	_, err := r.run("stop", id)
	return err
}

// Remove force-removes a container.
func (r *Runner) Remove(id string) error {
	_, err := r.run("rm", "-f", id)
	return err
}

// List returns all Orchestra-managed containers (filtered by the orchestra=1
// label), running or not.
func (r *Runner) List() ([]Container, error) {
	// Tab-separated, one container per line; stable field order.
	const format = "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Label \"orchestra.task\"}}"
	out, err := r.run("ps", "--all", "--filter", "label=orchestra=1", "--format", format)
	if err != nil {
		return nil, err
	}
	return parsePS(out), nil
}

// parsePS turns `docker ps` tab-separated output into Containers.
func parsePS(out string) []Container {
	var res []Container
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		for len(f) < 5 {
			f = append(f, "")
		}
		res = append(res, Container{
			ID:     f[0],
			Name:   f[1],
			Image:  f[2],
			Status: f[3],
			TaskID: f[4],
		})
	}
	return res
}
