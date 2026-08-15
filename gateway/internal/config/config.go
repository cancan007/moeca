// Package config loads and validates the Orchestra gateway configuration.
//
// The gateway is the single egress point for sandboxed agents: they never hold
// API keys or reach upstreams directly. Everything an agent may call is declared
// here as a "service" with its own upstream, allowlist, injected credentials,
// rate limit and token/cost budget.
package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

// Config is the top-level gateway configuration.
type Config struct {
	// Listen address, e.g. "0.0.0.0:8787". When the gateway runs as a container
	// on the sandbox egress network, it must bind all interfaces so sandboxes can
	// reach it by DNS name; the container publishes only 127.0.0.1:8787 to the
	// host for the UI, so this is not a broad exposure.
	Listen string `json:"listen"`
	// MaxBodyBytes caps request body size (0 => 8 MiB default).
	MaxBodyBytes int64 `json:"maxBodyBytes"`
	// RequestTimeoutSec bounds a single proxied request end-to-end (0 => 300s).
	RequestTimeoutSec int `json:"requestTimeoutSec"`
	// Sessions maps a session token to its identity. Requests must present a
	// valid token via the session header.
	Sessions map[string]Session `json:"sessions"`
	// Services maps a logical service name to its routing/security policy.
	Services map[string]Service `json:"services"`
	// AdminToken gates the provider admin API (X-Orchestra-Admin). When empty it
	// is taken from the ORCHESTRA_ADMIN_TOKEN environment variable at startup.
	// Sandboxes never receive it, so they cannot reach admin endpoints even
	// though they can reach the gateway port. Empty (both here and in env) =>
	// admin API disabled. Prefer AdminTokenSHA256 in production so the gateway
	// never holds the raw token.
	AdminToken string `json:"adminToken"`
	// AdminTokenSHA256 is the hex SHA-256 of the admin token. When set (or via
	// ORCHESTRA_ADMIN_TOKEN_SHA256), the gateway verifies presented tokens by
	// hash and never stores the raw token — the raw token lives only host-side
	// (the Tauri shell), so a leak of gateway memory/`docker inspect` reveals
	// only a useless hash. Takes precedence over AdminToken.
	AdminTokenSHA256 string `json:"adminTokenSha256"`
	// AllowPrivateTargets, when false (the default), makes the gateway refuse to
	// forward to loopback / RFC1918 / link-local / docker-host targets
	// (SSRF/host-reach protection). Set true only in tests that proxy to
	// loopback upstreams.
	AllowPrivateTargets bool `json:"allowPrivateTargets"`
	// CaptureContent enables recording request/response bodies (prompts, RAG,
	// tool I/O) into the access log for the monitoring plane. Nil => enabled
	// (personal/local scope). The content is only ever returned by the
	// admin-gated logs endpoint, never to a sandbox.
	CaptureContent *bool `json:"captureContent"`
	// Deny is a global forbidden-command policy applied to every service: a
	// request matching any rule is blocked (403) and audited. It complements the
	// per-service allowlists (deny-by-default writes, host allowlist) with an
	// explicit blocklist for "commands" (HTTP operations agents make through the
	// gateway) and network targets. The sandbox cannot bypass it — every egress
	// call passes here.
	Deny []DenyRule `json:"deny"`
}

// DenyRule blocks a class of gateway requests. Scope "command" matches the
// upstream-relative request path (prefix already stripped) with single-segment
// '*' globbing, optionally filtered by HTTP method; scope "network" matches the
// resolved upstream host by exact/dot-suffix name or CIDR.
type DenyRule struct {
	Scope   string   `json:"scope"`   // "command" | "network"
	Pattern string   `json:"pattern"` // command: path glob; network: host or CIDR
	Methods []string `json:"methods"` // command scope: optional method filter (empty => any)
	Note    string   `json:"note"`
}

// CommandDenied reports the first command-scope rule that blocks (method, path).
func (c *Config) CommandDenied(method, reqPath string) (DenyRule, bool) {
	for _, r := range c.Deny {
		if !strings.EqualFold(r.Scope, "command") {
			continue
		}
		if !globMatch(r.Pattern, reqPath) {
			continue
		}
		if len(r.Methods) > 0 {
			matched := false
			for _, m := range r.Methods {
				if strings.EqualFold(m, method) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		return r, true
	}
	return DenyRule{}, false
}

// NetworkDenied reports the first network-scope rule that blocks host. A rule
// with a '/' is treated as a CIDR (matched against an IP-literal host); otherwise
// it is an exact or dot-suffix host match.
func (c *Config) NetworkDenied(host string) (DenyRule, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i] // drop port
	}
	for _, r := range c.Deny {
		if !strings.EqualFold(r.Scope, "network") {
			continue
		}
		if strings.Contains(r.Pattern, "/") {
			if _, cidr, err := net.ParseCIDR(r.Pattern); err == nil {
				if ip := net.ParseIP(h); ip != nil && cidr.Contains(ip) {
					return r, true
				}
			}
			continue
		}
		if HostAllowed(h, []string{r.Pattern}) {
			return r, true
		}
	}
	return DenyRule{}, false
}

// Capture reports whether request/response content logging is enabled (default true).
func (c *Config) Capture() bool { return c.CaptureContent == nil || *c.CaptureContent }

// Session identifies an authenticated caller (one running agent context).
type Session struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Groups are the knowledge groups this session may retrieve, surfaced to
	// upstreams through the ${GROUPS} inject template.
	//
	// nil and empty mean different things and the distinction is load-bearing:
	// nil states no policy and the header is omitted, so the upstream searches
	// everything; an empty slice states a session entitled to nothing and the
	// header is sent empty. Collapsing the two would give a session with no
	// entitlements access to every group, so this field must be decoded from
	// JSON rather than defaulted.
	Groups []string `json:"groups"`
}

// Service is a single upstream the gateway will proxy to, keyed by URL prefix.
type Service struct {
	// Kind classifies the service: "model" (an LLM provider selectable by Solo
	// agents) or "tool" (github/fetch/etc). Empty is treated as "tool".
	Kind string `json:"kind"`
	// Models lists the model ids this provider offers (only meaningful for
	// Kind=="model"); the UI's Solo-agent model picker is driven by this.
	Models []string `json:"models"`
	// Prefix is the path prefix that routes to this service, e.g. "/anthropic/".
	Prefix string `json:"prefix"`
	// Upstream base URL, e.g. "https://api.anthropic.com". Empty means the
	// target host is dynamic (the /fetch/* pattern) and taken from the path.
	Upstream string `json:"upstream"`
	// Allowlist of permitted upstream hostnames. A request whose resolved host
	// is not listed is rejected. Supports leading-dot suffix match (".acme.com").
	Allowlist []string `json:"allowlist"`
	// InjectHeaders are set on the outbound request. Values may reference the
	// environment via ${VAR}; the secret never enters the agent's container.
	InjectHeaders map[string]string `json:"injectHeaders"`
	// StripHeaders are removed from the inbound request before forwarding
	// (e.g. a client-supplied Authorization that must not leak upstream).
	StripHeaders []string `json:"stripHeaders"`
	// RateLimit throttles requests per session for this service.
	RateLimit RateLimit `json:"rateLimit"`
	// Budget caps estimated token/cost spend per session for this service.
	Budget Budget `json:"budget"`
	// WriteAllow, when non-nil, puts the service in deny-by-default write mode:
	// GET/HEAD always pass, but any mutating request (POST/PUT/PATCH/DELETE/…)
	// is rejected unless it matches one of these rules. A nil WriteAllow leaves
	// every method allowed (the default for e.g. the Anthropic upstream, which
	// needs POST). Used to stop a sandboxed agent from merging PRs or pushing to
	// base branches through the injected GitHub token.
	WriteAllow []WriteRule `json:"writeAllow"`
	// AllowPaths, when non-empty, restricts which upstream-relative paths this
	// service will forward at all — every method, reads included.
	//
	// This is a different question from WriteAllow, which asks whether a request
	// may change something and therefore always lets GET through. Some upstreams
	// have read routes that are themselves privileged: the indexer's /status and
	// /graph enumerate every source in the index, which is exactly what a
	// group-scoped agent must not see, so filtering /search alone would leak the
	// catalogue through the front door.
	AllowPaths []string `json:"allowPaths"`
	// ProtectedBranches are branch names an agent may never create, update, or
	// merge (e.g. main/master/develop). Enforced as defense-in-depth on the
	// allowed branch-creation route via request-body inspection.
	ProtectedBranches []string `json:"protectedBranches"`
}

// WriteRule permits a class of mutating requests: any of Methods on a path that
// matches Path. Path is matched against the upstream-relative request path (the
// prefix already stripped) with single-segment '*' globbing, e.g.
// "/repos/*/*/pulls" matches "/repos/octo/hello/pulls".
type WriteRule struct {
	Methods []string `json:"methods"`
	Path    string   `json:"path"`
}

// RateLimit is a token-bucket policy.
type RateLimit struct {
	RPS   float64 `json:"rps"`   // sustained requests/sec (0 => unlimited)
	Burst int     `json:"burst"` // bucket size
}

// Budget caps estimated tokens spent per session on a service.
type Budget struct {
	// MaxTokensPerSession is the ceiling (0 => unlimited). Estimated as
	// (requestBytes + responseBytes) / BytesPerToken.
	MaxTokensPerSession int64 `json:"maxTokensPerSession"`
}

// Timeout returns the per-request timeout as a duration.
// Timeout bounds one proxied request.
//
// The default deliberately outlasts the agent's own model-call timeout (300s in
// agent/internal/llm). When the two are equal a timed-out call could have come
// from either side, and the log says only "deadline exceeded" — so the pair is
// staggered, and whichever gives up first is known before anyone reads a log.
// The agent is the one that should give up: it is the party that can retry.
func (c *Config) Timeout() time.Duration {
	if c.RequestTimeoutSec <= 0 {
		return 360 * time.Second
	}
	return time.Duration(c.RequestTimeoutSec) * time.Second
}

// Load reads, parses and validates a JSON config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := c.normalizeAndValidate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) normalizeAndValidate() error {
	if c.Listen == "" {
		c.Listen = "0.0.0.0:8787"
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 8 << 20 // 8 MiB
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("config: no services defined")
	}
	seen := map[string]string{}
	for name, svc := range c.Services {
		if svc.Prefix == "" || !strings.HasPrefix(svc.Prefix, "/") || !strings.HasSuffix(svc.Prefix, "/") {
			return fmt.Errorf("service %q: prefix must start and end with '/'", name)
		}
		if other, dup := seen[svc.Prefix]; dup {
			return fmt.Errorf("service %q and %q share prefix %q", name, other, svc.Prefix)
		}
		seen[svc.Prefix] = name
		if svc.Upstream == "" && len(svc.Allowlist) == 0 {
			return fmt.Errorf("service %q: dynamic upstream requires a non-empty allowlist", name)
		}
	}
	for i, r := range c.Deny {
		if !strings.EqualFold(r.Scope, "command") && !strings.EqualFold(r.Scope, "network") {
			return fmt.Errorf("deny[%d]: scope must be \"command\" or \"network\"", i)
		}
		if strings.TrimSpace(r.Pattern) == "" {
			return fmt.Errorf("deny[%d]: pattern is required", i)
		}
	}
	return nil
}

var envRef = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// ExpandEnv substitutes ${VAR} references from the process environment. Used at
// request time so injected secrets are read fresh and never logged verbatim.
func ExpandEnv(v string) string {
	return envRef.ReplaceAllStringFunc(v, func(m string) string {
		name := envRef.FindStringSubmatch(m)[1]
		return os.Getenv(name)
	})
}

// WriteControlled reports whether the service enforces a write allowlist. When
// false, every method is permitted (back-compat for upstreams that need POST).
func (s Service) WriteControlled() bool { return s.WriteAllow != nil }

// WriteAllowed reports whether a request (method, upstream-relative path) may
// proceed. Read methods (GET/HEAD) always pass. Mutating methods pass only when
// a WriteRule matches both the method and the path glob.
func (s Service) WriteAllowed(method, reqPath string) bool {
	if !s.WriteControlled() {
		return true
	}
	switch strings.ToUpper(method) {
	case "GET", "HEAD":
		return true
	}
	for _, r := range s.WriteAllow {
		if r.matches(method, reqPath) {
			return true
		}
	}
	return false
}

func (r WriteRule) matches(method, reqPath string) bool {
	methodOK := len(r.Methods) == 0
	for _, m := range r.Methods {
		if strings.EqualFold(m, method) {
			methodOK = true
			break
		}
	}
	if !methodOK {
		return false
	}
	return globMatch(r.Path, reqPath)
}

// globMatch matches a pattern against a path segment-by-segment. Each '*' in the
// pattern matches exactly one path segment (never crossing '/'), so
// "/repos/*/*/pulls" matches "/repos/o/r/pulls" but not "/repos/o/r/pulls/1".
func globMatch(pattern, p string) bool {
	pattern = "/" + strings.Trim(pattern, "/")
	p = "/" + strings.Trim(p, "/")
	ps := strings.Split(pattern, "/")
	xs := strings.Split(p, "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		ok, err := path.Match(ps[i], xs[i])
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// PathAllowed reports whether an upstream-relative path may be forwarded. A
// service with no AllowPaths forwards everything, which is the default for
// upstreams whose whole surface is meant to be reachable.
func (s Service) PathAllowed(reqPath string) bool {
	if len(s.AllowPaths) == 0 {
		return true
	}
	for _, p := range s.AllowPaths {
		if globMatch(p, reqPath) {
			return true
		}
	}
	return false
}

// IsProtectedRef reports whether a git ref (e.g. "refs/heads/main" or "main")
// names a protected branch. Comparison is case-insensitive on the branch name.
func (s Service) IsProtectedRef(ref string) bool {
	b := strings.TrimPrefix(ref, "refs/heads/")
	for _, p := range s.ProtectedBranches {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(b)) {
			return true
		}
	}
	return false
}

// HostAllowed reports whether host is permitted by allowlist. An entry may be an
// exact host ("api.acme.com") or a dot-suffix (".acme.com" matches any subdomain).
func HostAllowed(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // drop port
	}
	for _, a := range allowlist {
		a = strings.ToLower(strings.TrimSpace(a))
		if strings.HasPrefix(a, ".") {
			if host == a[1:] || strings.HasSuffix(host, a) {
				return true
			}
			continue
		}
		if host == a {
			return true
		}
	}
	return false
}
