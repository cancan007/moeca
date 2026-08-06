package docker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeDocker writes a stand-in `docker` that logs every invocation and models
// the two behaviours Create depends on: `inspect` reports whether a container
// exists and whether it is running, and unknown names exit non-zero.
//
// state: "" = no such container, "exited", or "running".
func fakeDocker(t *testing.T, state string) (r *Runner, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake is POSIX-only")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	bin := filepath.Join(dir, "docker")

	// Real `docker inspect -f {{.State.Running}}` prints exactly true/false and
	// nothing else, so the branch must exit rather than fall through.
	inspect := "exit 1" // unknown container
	switch state {
	case "exited":
		inspect = "echo false; exit 0"
	case "running":
		inspect = "echo true; exit 0"
	}
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + logPath + "\n" +
		"case \"$1\" in\n" +
		"  inspect) " + inspect + " ;;\n" +
		"esac\n" +
		"echo container-id-abc\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{bin: bin, timeout: 10 * time.Second}, logPath
}

func calls(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// A run that already finished keeps its containers so their logs can still be
// read. When a name does collide with one of those, clearing it is safe.
func TestCreateClearsExitedContainer(t *testing.T) {
	r, logPath := fakeDocker(t, "exited")

	if _, err := r.Create(Spec{TaskID: "web-app-planner", WorktreePath: t.TempDir()}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := calls(t, logPath)
	if len(got) != 3 {
		t.Fatalf("want inspect, rm, run — got %v", got)
	}
	if !strings.HasPrefix(got[0], "inspect ") {
		t.Errorf("call 1 = %q, want inspect", got[0])
	}
	if !strings.HasPrefix(got[1], "rm -f ") {
		t.Errorf("call 2 = %q, want rm -f", got[1])
	}
	if !strings.HasPrefix(got[2], "run -d ") {
		t.Errorf("call 3 = %q, want run -d", got[2])
	}
}

// The regression this guards: force-removing a *running* container would kill a
// live agent belonging to another run. Refuse instead, and touch nothing.
func TestCreateRefusesToKillRunningContainer(t *testing.T) {
	r, logPath := fakeDocker(t, "running")

	_, err := r.Create(Spec{TaskID: "web-app-planner", WorktreePath: t.TempDir()})
	if err == nil {
		t.Fatal("Create succeeded against a running container; want an error")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %v, want it to name the running container", err)
	}
	for _, c := range calls(t, logPath) {
		if strings.HasPrefix(c, "rm ") || strings.HasPrefix(c, "run -d ") {
			t.Errorf("must not %q while the container is running", c)
		}
	}
}

// The common case: nothing holds the name, so go straight to create.
func TestCreateSkipsRemovalWhenNameIsFree(t *testing.T) {
	r, logPath := fakeDocker(t, "")

	id, err := r.Create(Spec{TaskID: "fresh-task", WorktreePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "container-id-abc" {
		t.Errorf("id = %q, want container-id-abc", id)
	}
	for _, c := range calls(t, logPath) {
		if strings.HasPrefix(c, "rm ") {
			t.Errorf("unexpected removal when the name was free: %q", c)
		}
	}
}

// Spec.Name scopes the container name to a run while TaskID — which drives the
// orchestra.task label and List() grouping — stays run-independent.
func TestSpecNameOverridesContainerNameButNotLabel(t *testing.T) {
	spec := Spec{TaskID: "web-app-planner", Name: "orchestra-web-app-planner-run-abc123", WorktreePath: "/tmp/wt"}

	if got := spec.ContainerName(); got != "orchestra-web-app-planner-run-abc123" {
		t.Errorf("ContainerName() = %q, want the override", got)
	}
	args := RunArgs(spec)
	if !hasFlagValue(args, "--name", "orchestra-web-app-planner-run-abc123") {
		t.Errorf("--name should use Spec.Name: %v", args)
	}
	if !hasFlagValue(args, "--label", "orchestra.task=web-app-planner") {
		t.Errorf("orchestra.task label must stay run-independent: %v", args)
	}

	// Without an override the name still derives from TaskID.
	if got := (Spec{TaskID: "web-app-planner"}).ContainerName(); got != "orchestra-web-app-planner" {
		t.Errorf("default ContainerName() = %q", got)
	}
}
