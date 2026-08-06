package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"orchestra/sandbox/internal/docker"
)

// fakeDocker is a stub Client that records calls and returns canned results, so
// handler behavior can be tested without a running Docker daemon. It is
// mutex-guarded because the orchestrator drives it from concurrent goroutines.
type fakeDocker struct {
	mu         sync.Mutex
	lastSpec   docker.Spec
	createID   string
	createErr  error
	logs       string
	logsErr    error
	stopErr    error
	removeErr  error
	removed    []string
	stopped    []string
	list       []docker.Container
	listErr    error
	networks   []networkCall
	created    []docker.Spec                    // every Create, in call order (orchestrator tests)
	waitFn     func(taskID string) (int, error) // keyed by spec.TaskID; nil => exit 0
	createHook func(docker.Spec)                // optional: simulate the agent writing to the worktree
	resolved   []string                         // every Resolve, in call order
	resolveErr error
}

func (f *fakeDocker) Create(spec docker.Spec) (string, error) {
	f.mu.Lock()
	f.lastSpec = spec
	f.created = append(f.created, spec)
	err, id, hook := f.createErr, f.createID, f.createHook
	f.mu.Unlock()
	if hook != nil {
		hook(spec) // outside the lock: writes files into spec.WorktreePath
	}
	if err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	return "cid-" + spec.TaskID, nil
}
func (f *fakeDocker) Logs(id string) (string, error) { return f.logs, f.logsErr }
func (f *fakeDocker) Stop(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return f.stopErr
}
func (f *fakeDocker) Remove(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return f.removeErr
}
func (f *fakeDocker) List() ([]docker.Container, error) { return f.list, f.listErr }
func (f *fakeDocker) EnsureNetwork(name string, internal bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks = append(f.networks, networkCall{name: name, internal: internal})
	return nil
}

// Wait maps a container id ("cid-<taskID>") back to its taskID and defers to
// waitFn so a test can script per-stage exit codes.
func (f *fakeDocker) Wait(id string) (int, error) {
	f.mu.Lock()
	fn := f.waitFn
	f.mu.Unlock()
	if fn != nil {
		return fn(strings.TrimPrefix(id, "cid-"))
	}
	return 0, nil
}

// Resolve stands in for the host-side digest pin: it echoes a deterministic
// fake digest so tests can assert that what reaches docker run is the resolved
// id, not the (movable) tag the policy declared.
func (f *fakeDocker) Resolve(ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, ref)
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return "sha256:digest-of-" + ref, nil
}

type networkCall struct {
	name     string
	internal bool
}

func newTest(cfg *Config, cli Client) *httptest.Server {
	if cfg == nil {
		cfg = &Config{Image: "orchestra/agent:latest", Network: "orchestra-egress"}
	}
	return httptest.NewServer(newWith(cfg, cli).Handler())
}

func do(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var r *http.Request
	var err error
	if body != nil {
		b, _ := json.Marshal(body)
		r, err = http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
	} else {
		r, err = http.NewRequest(method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	return resp, out
}

func TestHealth(t *testing.T) {
	srv := newTest(nil, &fakeDocker{})
	defer srv.Close()
	resp, body := do(t, srv, "GET", "/health", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	if body["status"] != "ok" {
		t.Errorf("health body = %v", body)
	}
}

func TestEnsureNetworks(t *testing.T) {
	fake := &fakeDocker{}
	s := newWith(&Config{EgressNetwork: "orchestra-egress", RelaxedNetwork: "orchestra-relaxed"}, fake)
	if err := s.EnsureNetworks(); err != nil {
		t.Fatalf("EnsureNetworks: %v", err)
	}
	want := map[string]bool{"orchestra-egress": true, "orchestra-relaxed": false}
	if len(fake.networks) != 2 {
		t.Fatalf("networks provisioned = %d, want 2 (%+v)", len(fake.networks), fake.networks)
	}
	for _, n := range fake.networks {
		exp, ok := want[n.name]
		if !ok {
			t.Errorf("unexpected network %q", n.name)
		}
		if n.internal != exp {
			t.Errorf("network %q internal = %v, want %v", n.name, n.internal, exp)
		}
	}
}

func TestMethodAndRouteMismatch(t *testing.T) {
	srv := newTest(nil, &fakeDocker{})
	defer srv.Close()

	// wrong method on an existing path => 405 (ServeMux method routing)
	if resp, _ := do(t, srv, "GET", "/run/stop", nil); resp.StatusCode != 405 {
		t.Errorf("GET /run/stop status = %d, want 405", resp.StatusCode)
	}
	// unknown route => 404
	if resp, _ := do(t, srv, "GET", "/nope", nil); resp.StatusCode != 404 {
		t.Errorf("unknown route status = %d, want 404", resp.StatusCode)
	}
}

// The single-container routes are gone, but the spec they used to build is the
// same one every stage gets. These assertions moved off the HTTP surface rather
// than being dropped with it — they are the network posture, not an endpoint.

func TestBuildSpecAppliesConfigDefaultsAndMergesEnv(t *testing.T) {
	s := New(&Config{
		Image:   "orchestra/agent:latest",
		Network: "orchestra-egress",
		Env:     map[string]string{"ANTHROPIC_BASE_URL": "http://host.docker.internal:8787/anthropic"},
	})

	spec := s.buildSpec("web-app-feat", "/tmp/wt/feat", ImagePolicy{Ref: "orchestra/agent:latest"}, []string{"claude"}, map[string]string{"EXTRA": "1"}, false)

	if spec.TaskID != "web-app-feat" || spec.WorktreePath != "/tmp/wt/feat" {
		t.Errorf("spec not carried through: %+v", spec)
	}
	if spec.Image != "orchestra/agent:latest" {
		t.Errorf("image default not applied: %q", spec.Image)
	}
	if spec.Env["ANTHROPIC_BASE_URL"] == "" || spec.Env["EXTRA"] != "1" {
		t.Errorf("env not merged: %+v", spec.Env)
	}
}

// A stage must not be able to talk its way out of the egress network: under
// strict isolation the controller derives the gateway base URLs, overriding
// anything the caller supplied.
func TestBuildSpecStrictIsolationOverridesClientGatewayBase(t *testing.T) {
	s := New(&Config{
		Image:             "orchestra/agent:latest",
		EgressNetwork:     "orchestra-egress",
		RelaxedNetwork:    "orchestra-relaxed",
		GatewayStrictBase: "http://orchestra-gateway:8787",
		Env:               map[string]string{"ANTHROPIC_BASE_URL": "http://host.docker.internal:8787/anthropic"},
	})

	strict := s.buildSpec("t1", "/w", ImagePolicy{Ref: "orchestra/agent:latest"}, nil, map[string]string{"ANTHROPIC_BASE_URL": "http://evil/anthropic"}, true)
	if strict.Network != "orchestra-egress" {
		t.Errorf("strict network = %q, want orchestra-egress", strict.Network)
	}
	if got := strict.Env["ANTHROPIC_BASE_URL"]; got != "http://orchestra-gateway:8787/anthropic" {
		t.Errorf("strict ANTHROPIC_BASE_URL = %q; a client value must not override the strict gateway base", got)
	}

	relaxed := s.buildSpec("t2", "/w", ImagePolicy{Ref: "orchestra/agent:latest"}, nil, nil, false)
	if relaxed.Network != "orchestra-relaxed" {
		t.Errorf("relaxed network = %q, want orchestra-relaxed", relaxed.Network)
	}
	if got := relaxed.Env["ANTHROPIC_BASE_URL"]; got != "http://host.docker.internal:8787/anthropic" {
		t.Errorf("relaxed ANTHROPIC_BASE_URL = %q, want the host-loopback base", got)
	}
}
