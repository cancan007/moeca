// Package api exposes the sandbox service's HTTP control plane, consumed by the
// Orchestra host/frontend to launch and manage Docker sandboxes for agents.
//
// Each sandbox is a hardened container that can touch only its task's git
// worktree and holds no secrets (outbound calls go through the gateway). The
// server binds to loopback and runs as a Tauri sidecar next to the gateway and
// host agent.
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestra/sandbox/internal/docker"
)

// Client is the docker control surface the server depends on. *docker.Runner
// satisfies it in production; tests supply a fake to exercise handlers without
// a running Docker daemon.
type Client interface {
	Create(spec docker.Spec) (string, error)
	Logs(id string) (string, error)
	Stop(id string) error
	Remove(id string) error
	List() ([]docker.Container, error)
	EnsureNetwork(name string, internal bool) error
	Wait(id string) (int, error)
	// Resolve turns an image reference into the immutable digest it currently
	// points at, pulling host-side if needed.
	Resolve(ref string) (string, error)
}

// Config is the sandbox service configuration.
type Config struct {
	// Listen address, e.g. "127.0.0.1:8789". Bind to loopback only.
	Listen string `json:"listen"`
	// Image is the legacy single-image field, used as the "base" policy when
	// Images is empty so installs that predate the allowlist keep working.
	Image string `json:"image"`
	// Images is the container-image allowlist. A stage names a policy from this
	// list; it never sends an image reference, a network or a resource limit.
	// See images.go.
	Images []ImagePolicy `json:"images"`
	// MaxMemoryMB, MaxCPUs, MaxPidsLimit are the ceilings an image policy may
	// not exceed — a custom image added from Settings cannot ask for the whole
	// machine. Zero => the built-in ceilings.
	MaxMemoryMB  int     `json:"maxMemoryMB"`
	MaxCPUs      float64 `json:"maxCPUs"`
	MaxPidsLimit int     `json:"maxPidsLimit"`
	// Network is the default docker network for sandboxes. Deprecated in favour
	// of EgressNetwork/RelaxedNetwork; kept for back-compat as the strict fallback.
	Network string `json:"network"`
	// EgressNetwork is the internal (--internal) network used by strict sandboxes:
	// no route to the host or internet, only the gateway is reachable. Defaults to
	// Network, then "orchestra-egress".
	EgressNetwork string `json:"egressNetwork"`
	// RelaxedNetwork is the ordinary bridge network used by relaxed sandboxes
	// (future interactive use), which retains host/internet reachability. Defaults
	// to "orchestra-relaxed".
	RelaxedNetwork string `json:"relaxedNetwork"`
	// GatewayStrictBase is the gateway origin as seen from inside the egress
	// network, e.g. "http://orchestra-gateway:8787". The controller derives the
	// per-service base URLs (ANTHROPIC_BASE_URL, GITHUB_API_URL, ORCHESTRA_GATEWAY)
	// from it for strict sandboxes, so a client cannot point egress elsewhere.
	GatewayStrictBase string `json:"gatewayStrictBase"`
	// GatewayAdminBase is the gateway's LOOPBACK origin, used only to mint a
	// session per run (see runsession.go). It is deliberately a different URL
	// from GatewayStrictBase: the admin API is reachable from the host and never
	// from inside the egress island.
	GatewayAdminBase string `json:"gatewayAdminBase"`
	// RegistryStrictBase is the package-registry proxy origin as seen from inside
	// the egress network, e.g. "http://orchestra-registry:8791". The controller
	// derives NPM_CONFIG_REGISTRY / PIP_INDEX_URL / GOPROXY from it, so a stage
	// installing dependencies goes through the proxy and nowhere else.
	RegistryStrictBase string `json:"registryStrictBase"`
	// SessionToken authenticates the agent to the gateway (injected as
	// ORCHESTRA_SESSION). Must match a session configured in the gateway.
	SessionToken string `json:"sessionToken"`
	// MemoryMB, CPUs, PidsLimit are default per-sandbox resource caps.
	MemoryMB  int     `json:"memoryMB"`
	CPUs      float64 `json:"cpus"`
	PidsLimit int     `json:"pidsLimit"`
	// Env are default non-secret env vars injected into every sandbox (e.g.
	// gateway base URLs for the relaxed path). Never put secrets here.
	Env map[string]string `json:"env"`
	// MaxDelegateDepth bounds runtime supervisor delegation: a stage agent (depth
	// 0) may spawn sub-agents, but sub-agents (depth 1) may not delegate further.
	// 0 => default of 1.
	MaxDelegateDepth int `json:"maxDelegateDepth"`
	// DenyPaths is the forbidden-path policy ("path" scope) injected into every
	// agent as ORCHESTRA_DENY_PATHS — file-tool globs (e.g. "*.pem") the agent
	// must refuse to read/write.
	DenyPaths []string `json:"denyPaths"`
	// LogDir is where finished stages' logs are archived so they survive their
	// container. Empty => <tmp>/orchestra-logs.
	LogDir string `json:"logDir"`
	// LogRetentionDays bounds how long run archives are kept. A pointer so an
	// explicit 0 ("keep everything") is distinguishable from the field being
	// absent; nil => DefaultRetentionDays. An operator can override this at
	// runtime from Settings — see retention.go.
	LogRetentionDays *int `json:"logRetentionDays"`
}

// logDir returns the stage-log archive root (default <tmp>/orchestra-logs).
func (c *Config) logDir() string {
	return firstNonEmpty(c.LogDir, filepath.Join(os.TempDir(), "orchestra-logs"))
}

// maxDelegateDepth returns the delegation depth cap (default 1).
func (c *Config) maxDelegateDepth() int {
	if c.MaxDelegateDepth <= 0 {
		return 1
	}
	return c.MaxDelegateDepth
}

// Default network names and gateway origin, applied when a config omits them.
const (
	defaultEgressNetwork      = "orchestra-egress"
	defaultRelaxedNetwork     = "orchestra-relaxed"
	defaultGatewayStrictBase  = "http://orchestra-gateway:8787"
	defaultGatewayAdminBase   = "http://127.0.0.1:8787"
	defaultRegistryStrictBase = "http://orchestra-registry:8791"
)

// egressNetwork returns the configured internal network name (with fallbacks).
func (c *Config) egressNetwork() string {
	return firstNonEmpty(c.EgressNetwork, c.Network, defaultEgressNetwork)
}

// relaxedNetwork returns the configured bridge network name (with fallback).
func (c *Config) relaxedNetwork() string {
	return firstNonEmpty(c.RelaxedNetwork, defaultRelaxedNetwork)
}

// gatewayAdminBase returns the loopback gateway origin for the admin API.
func (c *Config) gatewayAdminBase() string {
	return strings.TrimRight(firstNonEmpty(c.GatewayAdminBase, defaultGatewayAdminBase), "/")
}

// gatewayStrictBase returns the in-egress-network gateway origin (with fallback).
func (c *Config) gatewayStrictBase() string {
	return firstNonEmpty(c.GatewayStrictBase, defaultGatewayStrictBase)
}

// registryStrictBase returns the in-egress-network package-registry proxy origin
// (with fallback).
func (c *Config) registryStrictBase() string {
	return firstNonEmpty(c.RegistryStrictBase, defaultRegistryStrictBase)
}

// Server implements the HTTP API.
type Server struct {
	cfg    *Config
	docker Client

	mu sync.Mutex

	rmu  sync.Mutex      // guards runs
	runs map[string]*Run // run id -> orchestrated template run
}

// New builds a Server backed by the real docker CLI.
func New(cfg *Config) *Server {
	return newWith(cfg, docker.New())
}

// newWith builds a Server with an injected docker Client (used by tests).
func newWith(cfg *Config, cli Client) *Server {
	return &Server{cfg: cfg, docker: cli, runs: map[string]*Run{}}
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	// Every run is an orchestrated template run over a generic Stage DAG — a
	// single agent is a one-stage template, so there is no separate
	// single-container surface.
	mux.HandleFunc("POST /run", s.handleRunCreate)
	mux.HandleFunc("GET /run", s.handleRunStatus)
	mux.HandleFunc("GET /run/logs", s.handleRunLogs)
	mux.HandleFunc("POST /run/stop", s.handleRunStop)
	mux.HandleFunc("DELETE /run", s.handleRunRemove)
	// Archive retention (how long finished runs' logs + metadata are kept).
	mux.HandleFunc("GET /retention", s.handleRetentionGet)
	mux.HandleFunc("POST /retention", s.handleRetentionSet)
	// The container-image allowlist: which images a stage may name, and which of
	// them are approved for unattended (scheduled) runs.
	mux.HandleFunc("GET /images", s.handleImagesGet)
	mux.HandleFunc("POST /images", s.handleImagesSet)
	mux.HandleFunc("DELETE /images", s.handleImagesDelete)
	return cors(mux)
}

// cors lets the local frontend (Vite dev server and the Tauri webview) call this
// loopback service from the browser. Loopback-only, so this is safe.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// idReq is the body shared by the run endpoints that address a run by id.
type idReq struct {
	ID string `json:"id"`
}

// buildSpec assembles a docker.Spec for one stage from the controller config and
// the stage's resolved image policy.
//
// Everything a client could try to influence is derived here, server-side:
//
//   - Isolation selects the network posture — strict attaches the sandbox to the
//     internal egress network so it can reach ONLY the gateway and the registry
//     proxy, relaxed uses an ordinary bridge. A policy may then narrow that
//     further to no network at all, never widen it.
//   - The gateway and registry base URLs are derived from the mode, so a client
//     cannot downgrade egress by supplying its own ANTHROPIC_BASE_URL or point
//     `npm install` at a registry of its choosing.
//   - Resource caps and scratch mounts come from the policy, clamped to the
//     configured ceilings.
func (s *Server) buildSpec(taskID, worktree string, policy ImagePolicy, cmd []string, reqEnv map[string]string, strict bool, session string) docker.Spec {
	network := s.cfg.relaxedNetwork()
	if strict {
		network = s.cfg.egressNetwork()
	}
	// A policy is allowed to be more restrictive than the run's isolation.
	if policy.Network == NetworkNone {
		network = NetworkNone
	}
	spec := docker.Spec{
		TaskID:       taskID,
		WorktreePath: worktree,
		Image:        firstNonEmpty(policy.Ref, s.cfg.Image),
		Cmd:          cmd,
		Env:          s.sandboxEnv(reqEnv, strict, policy.Network == NetworkNone, session),
		Network:      network,
		MemoryMB:     firstPositiveInt(policy.MemoryMB, s.cfg.MemoryMB),
		CPUs:         firstPositiveFloat(policy.CPUs, s.cfg.CPUs),
		PidsLimit:    firstPositiveInt(policy.PidsLimit, s.cfg.PidsLimit),
		Tmpfs:        policy.Tmpfs,
	}
	return spec
}

// networkVars are the environment variables that name something outside the
// container. A networkless stage has no route to any of them, so they are
// stripped rather than left to imply a reachability it does not have.
var networkVars = []string{
	"ANTHROPIC_BASE_URL", "GITHUB_API_URL", "ORCHESTRA_GATEWAY", "ORCHESTRA_SESSION",
	"ORCHESTRA_BASE_URL", "ORCHESTRA_REGISTRY",
	// Media generation is a gateway call like any other, so a networkless stage
	// must not be handed tools that describe an upstream it cannot reach.
	"ORCHESTRA_MEDIA",
	"NPM_CONFIG_REGISTRY", "PIP_INDEX_URL", "PIP_TRUSTED_HOST", "GOPROXY",
	"CARGO_REGISTRIES_CRATES_IO_INDEX", "CARGO_REGISTRIES_CRATES_IO_PROTOCOL",
}

// sandboxEnv builds the container environment: configured defaults, overlaid
// with the request's explicitly-provided (non-secret) vars, then the endpoints
// the controller forces.
//
// For a strict sandbox both the gateway base URLs AND the package-registry base
// URLs are overwritten with the in-egress-network origins, whatever the client
// supplied. The two are the same argument applied twice: a sandbox on the
// internal network can reach only those two services, and a client must not be
// able to point either egress path — model traffic or dependency installs —
// somewhere else. Redirecting `npm install` is the more valuable of the two to
// an attacker, since it decides which code ends up executing.
//
// A networkless stage (an image policy with network "none") gets none of them.
// Secrets never flow through here.
func (s *Server) sandboxEnv(reqEnv map[string]string, strict, networkless bool, session string) map[string]string {
	merged := make(map[string]string, len(s.cfg.Env)+len(reqEnv)+8)
	for k, v := range s.cfg.Env {
		merged[k] = v
	}
	for k, v := range reqEnv {
		merged[k] = v
	}
	// Forbidden-path policy: applied to every agent (not client-overridable).
	if len(s.cfg.DenyPaths) > 0 {
		merged["ORCHESTRA_DENY_PATHS"] = strings.Join(s.cfg.DenyPaths, ",")
	}
	if networkless {
		for _, k := range networkVars {
			delete(merged, k)
		}
	} else if strict {
		base := strings.TrimRight(s.cfg.gatewayStrictBase(), "/")
		merged["ANTHROPIC_BASE_URL"] = base + "/anthropic"
		merged["GITHUB_API_URL"] = base + "/github"
		merged["ORCHESTRA_GATEWAY"] = base
		// The run's own session when it has one (it carries the knowledge
		// scope); the shared one otherwise, exactly as before.
		if session != "" {
			merged["ORCHESTRA_SESSION"] = session
		} else if s.cfg.SessionToken != "" {
			merged["ORCHESTRA_SESSION"] = s.cfg.SessionToken
		}
		for k, v := range registryEnv(s.cfg.registryStrictBase()) {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// registryEnv points each package manager at the in-egress registry proxy.
//
// These are the standard override variables for npm, pip, the go command and
// cargo. Two deliberate omissions: GOSUMDB/GONOSUMDB are NOT set, so the go
// command still verifies module checksums (it fetches the sum database through
// the same proxy), and no *_AUTH or token variable exists — the proxy serves
// public registries and holds no credentials.
//
// PIP_TRUSTED_HOST is required because the proxy is reached over plain HTTP
// inside the egress island. That is not a downgrade: the island has no route to
// anything but the gateway and this proxy, and the proxy itself speaks TLS to
// the real registries on its upstream NIC.
func registryEnv(base string) map[string]string {
	base = strings.TrimRight(base, "/")
	host := base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	return map[string]string{
		"ORCHESTRA_REGISTRY":                  base,
		"NPM_CONFIG_REGISTRY":                 base + "/npm/",
		"PIP_INDEX_URL":                       base + "/pypi/simple/",
		"PIP_TRUSTED_HOST":                    host,
		"GOPROXY":                             base + "/go/",
		"CARGO_REGISTRIES_CRATES_IO_PROTOCOL": "sparse",
		"CARGO_REGISTRIES_CRATES_IO_INDEX":    "sparse+" + base + "/crates/index/",
	}
}

// EnsureNetworks provisions the sandbox networks (idempotent). The egress network
// is internal (no host/internet route); the relaxed network is an ordinary bridge.
func (s *Server) EnsureNetworks() error {
	if err := s.docker.EnsureNetwork(s.cfg.egressNetwork(), true); err != nil {
		return err
	}
	return s.docker.EnsureNetwork(s.cfg.relaxedNetwork(), false)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstPositiveInt / firstPositiveFloat let an image policy override a
// controller default while leaving 0 to mean "use the default".
func firstPositiveInt(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstPositiveFloat(vals ...float64) float64 {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, 400, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Run starts the HTTP server (blocking). It first provisions the sandbox
// networks; a provisioning failure (e.g. Docker unavailable) is logged but not
// fatal, so the control plane still serves and reports the error per-request.
func (s *Server) Run() error {
	if err := s.EnsureNetworks(); err != nil {
		log.Printf("sandbox: network provisioning failed (sandboxes will error until fixed): %v", err)
	}
	s.startArchivePruner()
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	return srv.ListenAndServe()
}
