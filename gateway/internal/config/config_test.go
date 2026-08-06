package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("TOK", "abc123")
	cases := map[string]string{
		"${FOO}":            "bar",
		"Bearer ${TOK}":     "Bearer abc123",
		"${FOO}-${TOK}":     "bar-abc123",
		"no refs here":      "no refs here",
		"${MISSING}":        "",
	}
	for in, want := range cases {
		if got := ExpandEnv(in); got != want {
			t.Errorf("ExpandEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostAllowed(t *testing.T) {
	allow := []string{"api.acme.com", ".githubusercontent.com"}
	yes := []string{"api.acme.com", "api.acme.com:443", "raw.githubusercontent.com", "githubusercontent.com"}
	no := []string{"evil.com", "api.acme.com.evil.com", "acme.com"}
	for _, h := range yes {
		if !HostAllowed(h, allow) {
			t.Errorf("HostAllowed(%q) = false, want true", h)
		}
	}
	for _, h := range no {
		if HostAllowed(h, allow) {
			t.Errorf("HostAllowed(%q) = true, want false", h)
		}
	}
}

func TestWriteAllowedAndProtectedRef(t *testing.T) {
	// No WriteAllow => every method permitted (back-compat).
	open := Service{}
	if !open.WriteAllowed("POST", "/v1/messages") {
		t.Error("uncontrolled service should allow POST")
	}

	gh := Service{
		WriteAllow: []WriteRule{
			{Methods: []string{"POST"}, Path: "/repos/*/*/pulls"},
			{Methods: []string{"POST"}, Path: "/repos/*/*/git/refs"},
		},
		ProtectedBranches: []string{"main", "develop"},
	}
	allow := map[string]bool{
		"GET /repos/o/r":                true, // read always ok
		"POST /repos/o/r/pulls":         true, // create PR
		"POST /repos/o/r/git/refs":      true, // create branch
		"PUT /repos/o/r/pulls/1/merge":  false,
		"PUT /repos/o/r/contents/x.txt": false,
		"POST /repos/o/r/merges":        false,
		"POST /repos/o/r/pulls/1":       false, // extra segment must not match glob
	}
	for k, want := range allow {
		parts := splitMethodPath(k)
		if got := gh.WriteAllowed(parts[0], parts[1]); got != want {
			t.Errorf("WriteAllowed(%q) = %v, want %v", k, got, want)
		}
	}

	if !gh.IsProtectedRef("refs/heads/main") || !gh.IsProtectedRef("develop") {
		t.Error("protected refs not detected")
	}
	if gh.IsProtectedRef("refs/heads/feature-x") {
		t.Error("feature branch wrongly flagged as protected")
	}
}

func splitMethodPath(s string) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return [2]string{s[:i], s[i+1:]}
		}
	}
	return [2]string{s, ""}
}

func TestLoadValidates(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{
	  "services": { "a": { "prefix": "/a/", "upstream": "https://x.example", "allowlist": ["x.example"] } }
	}`), 0o600)
	cfg, err := Load(good)
	if err != nil {
		t.Fatalf("Load good: %v", err)
	}
	if cfg.Listen != "0.0.0.0:8787" {
		t.Errorf("default listen = %q", cfg.Listen)
	}
	if cfg.MaxBodyBytes != 8<<20 {
		t.Errorf("default maxBodyBytes = %d", cfg.MaxBodyBytes)
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"services":{"a":{"prefix":"noslash","upstream":"https://x"}}}`), 0o600)
	if _, err := Load(bad); err == nil {
		t.Errorf("Load bad prefix: expected error")
	}

	dyn := filepath.Join(dir, "dyn.json")
	os.WriteFile(dyn, []byte(`{"services":{"f":{"prefix":"/f/","upstream":""}}}`), 0o600)
	if _, err := Load(dyn); err == nil {
		t.Errorf("Load dynamic without allowlist: expected error")
	}
}

func TestCommandDenied(t *testing.T) {
	c := &Config{Deny: []DenyRule{
		{Scope: "command", Pattern: "/repos/*/*", Methods: []string{"DELETE"}},
		{Scope: "command", Pattern: "/admin/*"}, // any method
	}}
	cases := []struct {
		method, path string
		want         bool
	}{
		{"DELETE", "/repos/o/r", true},
		{"GET", "/repos/o/r", false},      // method filter excludes GET
		{"DELETE", "/repos/o/r/x", false}, // extra segment must not match glob
		{"POST", "/admin/reset", true},    // no method filter => any
		{"GET", "/admin/reset", true},
		{"GET", "/safe", false},
	}
	for _, tc := range cases {
		if _, got := c.CommandDenied(tc.method, tc.path); got != tc.want {
			t.Errorf("CommandDenied(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestNetworkDenied(t *testing.T) {
	c := &Config{Deny: []DenyRule{
		{Scope: "network", Pattern: "10.0.0.0/8"},
		{Scope: "network", Pattern: ".internal"},
		{Scope: "network", Pattern: "169.254.169.254"},
	}}
	cases := []struct {
		host string
		want bool
	}{
		{"10.1.2.3", true},
		{"10.1.2.3:443", true}, // port dropped
		{"8.8.8.8", false},
		{"metrics.internal", true},
		{"api.example.com", false},
		{"169.254.169.254", true},
	}
	for _, tc := range cases {
		if _, got := c.NetworkDenied(tc.host); got != tc.want {
			t.Errorf("NetworkDenied(%s) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestDenyValidation(t *testing.T) {
	base := func() *Config {
		return &Config{Services: map[string]Service{"a": {Prefix: "/a/", Upstream: "https://x"}}}
	}
	c := base()
	c.Deny = []DenyRule{{Scope: "command", Pattern: "/x/*"}}
	if err := c.normalizeAndValidate(); err != nil {
		t.Errorf("valid deny rule rejected: %v", err)
	}
	c = base()
	c.Deny = []DenyRule{{Scope: "bogus", Pattern: "x"}}
	if err := c.normalizeAndValidate(); err == nil {
		t.Error("bad scope should fail validation")
	}
	c = base()
	c.Deny = []DenyRule{{Scope: "command", Pattern: "  "}}
	if err := c.normalizeAndValidate(); err == nil {
		t.Error("empty pattern should fail validation")
	}
}
