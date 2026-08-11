package api

import (
	"errors"
	"strings"
	"testing"

	"orchestra/sandbox/internal/docker"
)

// imagesConfig is a controller with the three shipped images plus room for
// custom ones, backed by a temp archive dir so persistence is per-test.
func imagesConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		LogDir: t.TempDir(),
		Images: []ImagePolicy{
			{Name: "base", Ref: "orchestra/agent:latest", Unattended: true},
			{Name: "poly", Ref: "orchestra/agent-poly:latest", Unattended: true, MemoryMB: 4096, CPUs: 4, Tmpfs: []string{"/home/agent/.npm"}},
			{Name: "media", Ref: "orchestra/agent-media:latest", Unattended: true, Network: NetworkNone, MemoryMB: 6144},
		},
		EgressNetwork:      "orchestra-egress",
		RelaxedNetwork:     "orchestra-relaxed",
		GatewayStrictBase:  "http://orchestra-gateway:8787",
		RegistryStrictBase: "http://orchestra-registry:8791",
		SessionToken:       "sess",
	}
}

func TestResolveImage_DefaultAndUnknown(t *testing.T) {
	s := newWith(imagesConfig(t), &fakeDocker{})

	p, err := s.resolveImage("", false)
	if err != nil || p.Name != DefaultImageName {
		t.Fatalf("empty image = (%q, %v), want the default policy", p.Name, err)
	}
	if p, err := s.resolveImage("media", false); err != nil || p.Network != NetworkNone {
		t.Errorf("media = (%+v, %v), want the networkless policy", p, err)
	}
	// An unknown name must fail rather than quietly fall back: substituting a
	// different image is exactly what the allowlist exists to prevent.
	_, err = s.resolveImage("does-not-exist", false)
	if err == nil {
		t.Fatal("unknown image name was accepted")
	}
	if !strings.Contains(err.Error(), "allowed:") {
		t.Errorf("error should list the allowed images, got %q", err)
	}
}

// A config that predates the allowlist has only `image`; it must keep working,
// with that single reference becoming the default policy.
func TestResolveImage_LegacySingleImageConfig(t *testing.T) {
	s := newWith(&Config{Image: "orchestra/agent:latest", LogDir: t.TempDir()}, &fakeDocker{})
	p, err := s.resolveImage("", true)
	if err != nil {
		t.Fatalf("legacy config: %v", err)
	}
	if p.Ref != "orchestra/agent:latest" {
		t.Errorf("legacy ref = %q", p.Ref)
	}
}

// The unattended axis: a custom image is usable from an attended run (a
// reviewer is watching) but must be promoted before a schedule can pick it up.
func TestResolveImage_UnattendedRequiresPromotion(t *testing.T) {
	cfg := imagesConfig(t)
	s := newWith(cfg, &fakeDocker{})
	if err := s.saveCustomImages([]ImagePolicy{
		{Name: "my-debug", Ref: "ghcr.io/me/debug:1", Custom: true},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.resolveImage("my-debug", false); err != nil {
		t.Errorf("attended run rejected a custom image: %v", err)
	}
	_, err := s.resolveImage("my-debug", true)
	if err == nil {
		t.Fatal("an unpromoted custom image was accepted for a scheduled run")
	}
	if !strings.Contains(err.Error(), "promote") {
		t.Errorf("error should say how to fix it, got %q", err)
	}

	// After promotion the same image is allowed.
	if err := s.saveCustomImages([]ImagePolicy{
		{Name: "my-debug", Ref: "ghcr.io/me/debug:1", Custom: true, Unattended: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveImage("my-debug", true); err != nil {
		t.Errorf("promoted image still rejected: %v", err)
	}
}

func TestImagePolicyValidation(t *testing.T) {
	cfg := imagesConfig(t)
	cases := []struct {
		name   string
		policy ImagePolicy
		reason string
	}{
		{"empty name", ImagePolicy{Ref: "x"}, "name is required"},
		{"uppercase name", ImagePolicy{Name: "Debug", Ref: "x"}, "lowercase"},
		{"empty ref", ImagePolicy{Name: "a"}, "ref is required"},
		{"flag-like ref", ImagePolicy{Name: "a", Ref: "--privileged"}, "'-'"},
		{"whitespace ref", ImagePolicy{Name: "a", Ref: "img --net host"}, "whitespace"},
		// A policy may only be more restrictive than the run, never pick a
		// network of its own.
		{"arbitrary network", ImagePolicy{Name: "a", Ref: "x", Network: "host"}, "network must be"},
		{"bridge network", ImagePolicy{Name: "a", Ref: "x", Network: "orchestra-relaxed"}, "network must be"},
		{"relative tmpfs", ImagePolicy{Name: "a", Ref: "x", Tmpfs: []string{"cache"}}, "absolute"},
		{"root tmpfs", ImagePolicy{Name: "a", Ref: "x", Tmpfs: []string{"/"}}, "shadow"},
		{"worktree tmpfs", ImagePolicy{Name: "a", Ref: "x", Tmpfs: []string{"/work/node_modules"}}, "worktree"},
		{"negative memory", ImagePolicy{Name: "a", Ref: "x", MemoryMB: -1}, "negative"},
	}
	for _, c := range cases {
		if _, err := c.policy.normalize(cfg); err == nil {
			t.Errorf("%s: accepted, want a rejection mentioning %q", c.name, c.reason)
		} else if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: error = %q, want it to mention %q", c.name, err, c.reason)
		}
	}
}

// A custom image must not be able to claim the whole machine.
func TestImagePolicyClampsToCeilings(t *testing.T) {
	cfg := imagesConfig(t)
	cfg.MaxMemoryMB, cfg.MaxCPUs, cfg.MaxPidsLimit = 4096, 4, 1024

	p, err := ImagePolicy{Name: "greedy", Ref: "x", MemoryMB: 1 << 20, CPUs: 64, PidsLimit: 1 << 20}.normalize(cfg)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.MemoryMB != 4096 || p.CPUs != 4 || p.PidsLimit != 1024 {
		t.Errorf("clamped policy = %+v, want the ceilings", p)
	}
}

func TestImagesAPI_CustomLifecycle(t *testing.T) {
	cfg := imagesConfig(t)
	srv := newTest(cfg, &fakeDocker{})
	defer srv.Close()

	resp, out := do(t, srv, "GET", "/images", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET /images = %d", resp.StatusCode)
	}
	if names := imageNamesFrom(out); len(names) != 3 {
		t.Fatalf("built-in images = %v, want 3", names)
	}

	// add
	resp, _ = do(t, srv, "POST", "/images", map[string]any{
		"name": "my-debug", "ref": "ghcr.io/me/debug:1", "description": "rust toolchain",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("POST /images = %d", resp.StatusCode)
	}
	_, out = do(t, srv, "GET", "/images", nil)
	if !hasImage(out, "my-debug") {
		t.Fatal("custom image not listed after POST")
	}

	// a custom image is NOT unattended-capable until promoted
	for _, img := range imagesFrom(out) {
		if img["name"] == "my-debug" {
			if img["unattended"] == true {
				t.Error("a newly added custom image must not be unattended by default")
			}
			if img["custom"] != true {
				t.Error("a runtime-added image must be flagged custom")
			}
		}
	}

	// promote
	resp, _ = do(t, srv, "POST", "/images", map[string]any{
		"name": "my-debug", "ref": "ghcr.io/me/debug:1", "unattended": true,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("promote = %d", resp.StatusCode)
	}
	_, out = do(t, srv, "GET", "/images", nil)
	for _, img := range imagesFrom(out) {
		if img["name"] == "my-debug" && img["unattended"] != true {
			t.Error("promotion did not take effect")
		}
	}

	// delete
	resp, _ = do(t, srv, "DELETE", "/images?name=my-debug", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("DELETE /images = %d", resp.StatusCode)
	}
	_, out = do(t, srv, "GET", "/images", nil)
	if hasImage(out, "my-debug") {
		t.Error("custom image still listed after DELETE")
	}
}

// Settings edits runtime policies. Letting it rewrite a shipped one would make
// the baseline the install ships with a moving target.
func TestImagesAPI_BuiltInsAreNotEditable(t *testing.T) {
	srv := newTest(imagesConfig(t), &fakeDocker{})
	defer srv.Close()

	resp, out := do(t, srv, "POST", "/images", map[string]any{"name": "base", "ref": "ghcr.io/me/anything:1"})
	if resp.StatusCode != 400 {
		t.Fatalf("redefining a built-in = %d, want 400", resp.StatusCode)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "built-in") {
		t.Errorf("error = %q, want it to explain the name is built in", msg)
	}

	resp, _ = do(t, srv, "DELETE", "/images?name=base", nil)
	if resp.StatusCode != 404 {
		t.Errorf("deleting a built-in = %d, want 404", resp.StatusCode)
	}
}

// The whole point of the policy table: a stage names a policy, and the network
// posture, caps and scratch mounts come from the controller, not the client.
func TestBuildSpecAppliesTheImagePolicy(t *testing.T) {
	s := newWith(imagesConfig(t), &fakeDocker{})

	poly, err := s.resolveImage("poly", false)
	if err != nil {
		t.Fatal(err)
	}
	spec := s.buildSpec("t", "/w", poly, []string{"bash", "-lc", "npm ci"}, nil, true)
	if spec.MemoryMB != 4096 || spec.CPUs != 4 {
		t.Errorf("policy limits not applied: memory=%d cpus=%v", spec.MemoryMB, spec.CPUs)
	}
	if len(spec.Tmpfs) != 1 || spec.Tmpfs[0] != "/home/agent/.npm" {
		t.Errorf("policy tmpfs not applied: %v", spec.Tmpfs)
	}
	if strings.Join(spec.Cmd, " ") != "bash -lc npm ci" {
		t.Errorf("stage command not passed through: %v", spec.Cmd)
	}
	if spec.Network != "orchestra-egress" {
		t.Errorf("network = %q, want the egress island", spec.Network)
	}
}

// A media stage transcodes untrusted input. It has no reason to reach even the
// gateway, and nothing in its environment should suggest otherwise.
func TestBuildSpecNetworklessPolicyDropsEveryEndpoint(t *testing.T) {
	cfg := imagesConfig(t)
	cfg.Env = map[string]string{"ANTHROPIC_BASE_URL": "http://host.docker.internal:8787/anthropic"}
	s := newWith(cfg, &fakeDocker{})

	media, err := s.resolveImage("media", false)
	if err != nil {
		t.Fatal(err)
	}
	spec := s.buildSpec("t", "/w", media, []string{"ffmpeg"}, map[string]string{"ORCHESTRA_GATEWAY": "http://evil"}, true)

	if spec.Network != NetworkNone {
		t.Errorf("network = %q, want %q", spec.Network, NetworkNone)
	}
	for _, k := range networkVars {
		if v, ok := spec.Env[k]; ok {
			t.Errorf("networkless stage still carries %s=%q", k, v)
		}
	}
	// Non-network config still applies.
	if spec.MemoryMB != 6144 {
		t.Errorf("media memory = %d, want 6144", spec.MemoryMB)
	}
}

// Redirecting `npm install` decides which code ends up executing, so the
// registry endpoints are forced by the controller exactly like the gateway's.
func TestSandboxEnvForcesTheRegistryProxy(t *testing.T) {
	s := newWith(imagesConfig(t), &fakeDocker{})
	env := s.sandboxEnv(map[string]string{
		"NPM_CONFIG_REGISTRY": "https://evil.example/npm/",
		"GOPROXY":             "https://evil.example/go/",
		"PIP_INDEX_URL":       "https://evil.example/simple/",
	}, true, false)

	want := map[string]string{
		"NPM_CONFIG_REGISTRY":              "http://orchestra-registry:8791/npm/",
		"PIP_INDEX_URL":                    "http://orchestra-registry:8791/pypi/simple/",
		"PIP_TRUSTED_HOST":                 "orchestra-registry",
		"GOPROXY":                          "http://orchestra-registry:8791/go/",
		"CARGO_REGISTRIES_CRATES_IO_INDEX": "sparse+http://orchestra-registry:8791/crates/index/",
		"ORCHESTRA_REGISTRY":               "http://orchestra-registry:8791",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q (a client value must not survive)", k, env[k], v)
		}
	}
	// Checksum verification must stay on: nothing here may disable the go sum
	// database, which is fetched through the same proxy.
	for _, k := range []string{"GOSUMDB", "GONOSUMDB", "GOFLAGS", "GONOSUMCHECK"} {
		if v, ok := env[k]; ok {
			t.Errorf("%s=%q was set; module checksum verification must not be weakened", k, v)
		}
	}
}

// A bad image is a 400 at admission, not a run that dies at stage 7 having
// already produced half a deliverable.
func TestRunRejectsAnUnknownImageBeforeStarting(t *testing.T) {
	fake := &fakeDocker{}
	srv := newTest(imagesConfig(t), fake)
	defer srv.Close()

	body := chainReq(map[string]any{"id": "a", "name": "a", "role": "a", "image": "nope"})
	resp, out := do(t, srv, "POST", "/run", body)
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, `stage "a"`) {
		t.Errorf("error = %q, want it to name the offending stage", msg)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 0 {
		t.Errorf("a container was created for a rejected run: %+v", fake.created)
	}
}

// A schedule firing at 3am must not be able to reach an image a human added
// while debugging. The check happens at admission, for every stage.
func TestUnattendedRunRejectsAnUnpromotedImage(t *testing.T) {
	cfg := imagesConfig(t)
	s := newWith(cfg, &fakeDocker{})
	if err := s.saveCustomImages([]ImagePolicy{{Name: "my-debug", Ref: "ghcr.io/me/debug:1", Custom: true}}); err != nil {
		t.Fatal(err)
	}
	srv := newTest(cfg, &fakeDocker{})
	defer srv.Close()

	body := chainReq(map[string]any{"id": "a", "name": "a", "role": "a", "image": "my-debug"})

	// Attended (Delivery): allowed.
	if _, code := startRun(t, srv, body); code != 201 {
		t.Errorf("attended run status = %d, want 201", code)
	}

	// Unattended (Daily): refused until promoted.
	body["unattended"] = true
	resp, out := do(t, srv, "POST", "/run", body)
	if resp.StatusCode != 400 {
		t.Fatalf("unattended run status = %d, want 400", resp.StatusCode)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "scheduled runs") {
		t.Errorf("error = %q, want it to explain the unattended restriction", msg)
	}
}

// A tag moves. What reaches `docker run`, and what the archive records, must be
// the digest the tag resolved to at launch.
func TestStageRunsThePinnedDigest(t *testing.T) {
	fake := &fakeDocker{}
	srv := newTest(imagesConfig(t), fake)
	defer srv.Close()

	id, code := startRun(t, srv, chainReq(map[string]any{
		"id": "build", "name": "build", "role": "build",
		"image": "poly", "cmd": []string{"bash", "-lc", "npm ci && npm test"},
	}))
	if code != 201 {
		t.Fatalf("run status = %d", code)
	}
	run := waitRun(t, srv, id)

	fake.mu.Lock()
	specs := append([]docker.Spec(nil), fake.created...)
	resolved := append([]string(nil), fake.resolved...)
	fake.mu.Unlock()

	if len(specs) != 1 {
		t.Fatalf("created %d containers, want 1", len(specs))
	}
	if want := "sha256:digest-of-orchestra/agent-poly:latest"; specs[0].Image != want {
		t.Errorf("launched image = %q, want the resolved digest %q", specs[0].Image, want)
	}
	if len(resolved) != 1 || resolved[0] != "orchestra/agent-poly:latest" {
		t.Errorf("Resolve calls = %v, want the policy's tag", resolved)
	}
	if strings.Join(specs[0].Cmd, " ") != "bash -lc npm ci && npm test" {
		t.Errorf("stage command = %v", specs[0].Cmd)
	}

	stages, _ := run["stages"].([]any)
	st, _ := stages[0].(map[string]any)
	if st["image"] != "poly" {
		t.Errorf("archived image = %v, want \"poly\"", st["image"])
	}
	if digest, _ := st["imageDigest"].(string); !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("archived imageDigest = %q, want the resolved digest", digest)
	}
}

// If the image cannot be resolved (bad ref, registry down), the stage fails
// with a message naming the image rather than a bare docker error.
func TestStageFailsClearlyWhenTheImageCannotBeResolved(t *testing.T) {
	fake := &fakeDocker{resolveErr: errors.New("manifest unknown")}
	srv := newTest(imagesConfig(t), fake)
	defer srv.Close()

	id, code := startRun(t, srv, chainReq(stage("a")))
	if code != 201 {
		t.Fatalf("run status = %d", code)
	}
	run := waitRun(t, srv, id)
	if run["status"] != statusFailed {
		t.Errorf("run status = %v, want failed", run["status"])
	}
	// The reason has to reach the stage. There is no container and therefore no
	// container log, so if this is dropped the run reports nothing but
	// exitCode -1 and the cause has to be guessed from what is missing.
	stages, _ := run["stages"].([]any)
	st, _ := stages[0].(map[string]any)
	msg, _ := st["error"].(string)
	if !strings.Contains(msg, "manifest unknown") {
		t.Errorf("stage error = %q, want it to carry the docker failure", msg)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 0 {
		t.Error("a container was created despite the image failing to resolve")
	}
}

func imagesFrom(out map[string]any) []map[string]any {
	raw, _ := out["images"].([]any)
	res := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			res = append(res, m)
		}
	}
	return res
}

func imageNamesFrom(out map[string]any) []string {
	var names []string
	for _, m := range imagesFrom(out) {
		n, _ := m["name"].(string)
		names = append(names, n)
	}
	return names
}

func hasImage(out map[string]any, name string) bool {
	for _, n := range imageNamesFrom(out) {
		if n == name {
			return true
		}
	}
	return false
}
