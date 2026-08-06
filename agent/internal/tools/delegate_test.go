package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpawnSubagentWritesRequestAndReadsResult(t *testing.T) {
	root := t.TempDir()
	reg := New(root)
	reg.EnableDelegation()
	reg.delegateTimeout = 2 * time.Second
	reg.delegatePoll = 20 * time.Millisecond

	// spawn_subagent must be advertised once delegation is enabled
	found := false
	for _, d := range reg.Definitions() {
		if d.Name == "spawn_subagent" {
			found = true
		}
	}
	if !found {
		t.Fatal("spawn_subagent not advertised after EnableDelegation")
	}

	// A fake controller: wait for the request, then write a result.
	base := filepath.Join(root, delegateDir)
	go func() {
		for i := 0; i < 200; i++ {
			entries, _ := os.ReadDir(base)
			for _, e := range entries {
				dir := filepath.Join(base, e.Name())
				if _, err := os.Stat(filepath.Join(dir, "request.json")); err != nil {
					continue
				}
				raw, _ := os.ReadFile(filepath.Join(dir, "request.json"))
				var req map[string]string
				json.Unmarshal(raw, &req)
				if req["task"] != "build the thing" {
					t.Errorf("request task = %q", req["task"])
				}
				res, _ := json.Marshal(map[string]any{"result": "built it", "exitCode": 0})
				os.WriteFile(filepath.Join(dir, "result.json"), res, 0o644)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	out, isErr := reg.Dispatch("spawn_subagent", map[string]any{"role": "builder", "task": "build the thing"})
	if isErr {
		t.Fatalf("spawn_subagent returned error: %s", out)
	}
	if out != "built it" {
		t.Errorf("result = %q, want \"built it\"", out)
	}
}

func TestSpawnSubagentDisabledByDefault(t *testing.T) {
	reg := New(t.TempDir())
	for _, d := range reg.Definitions() {
		if d.Name == "spawn_subagent" {
			t.Fatal("spawn_subagent should not be advertised without EnableDelegation")
		}
	}
	out, isErr := reg.Dispatch("spawn_subagent", map[string]any{"role": "x", "task": "y"})
	if !isErr {
		t.Errorf("disabled spawn_subagent should error, got %q", out)
	}
}

func TestSpawnSubagentPropagatesError(t *testing.T) {
	root := t.TempDir()
	reg := New(root)
	reg.EnableDelegation()
	reg.delegateTimeout = 2 * time.Second
	reg.delegatePoll = 20 * time.Millisecond

	base := filepath.Join(root, delegateDir)
	go func() {
		for i := 0; i < 200; i++ {
			entries, _ := os.ReadDir(base)
			for _, e := range entries {
				dir := filepath.Join(base, e.Name())
				if _, err := os.Stat(filepath.Join(dir, "request.json")); err != nil {
					continue
				}
				res, _ := json.Marshal(map[string]any{"error": "child crashed", "exitCode": 1})
				os.WriteFile(filepath.Join(dir, "result.json"), res, 0o644)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	out, isErr := reg.Dispatch("spawn_subagent", map[string]any{"role": "r", "task": "t"})
	if !isErr {
		t.Errorf("expected error result, got success: %q", out)
	}
}
