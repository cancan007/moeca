// Command agent is the Orchestra agent runtime — the container entrypoint that
// executes an agentic coding task against the mounted git worktree at /work.
//
// It holds no credentials: every call to Claude's Messages API goes through the
// Orchestra security gateway (ANTHROPIC_BASE_URL), which injects the API key.
// The agent reads its task, runs a tool-use loop that edits files in /work via
// scoped tools, and emits A2A-style JSON log lines to stdout for `docker logs`.
//
// Configuration (all via environment, matching the sandbox's injected env):
//
//	ANTHROPIC_BASE_URL  gateway Anthropic prefix (default http://host.docker.internal:8787/anthropic)
//	ORCHESTRA_MODEL     model id (default claude-opus-4-8)
//	ORCHESTRA_SYSTEM    persona/system prompt (default: a short coding-agent prompt)
//	ORCHESTRA_TASK      the task prompt (overrides the task file if set)
//	ORCHESTRA_WORKDIR   worktree root (default /work)
//	ORCHESTRA_PROMPT_RAW        "1" keeps ORCHESTRA_SYSTEM verbatim (skip the composed frame)
//	ORCHESTRA_MAX_CONTEXT_TOKENS  context size that triggers history summarization (default 120000; 0 disables)
//	ORCHESTRA_KEEP_RECENT         trailing turns kept verbatim on compaction (default 6)
//	ORCHESTRA_EFFORT              reasoning effort low|medium|high|xhigh|max (default medium; the primary cost lever)
//	ORCHESTRA_MAX_TOKENS          per-response output-token cap (default 16000)
//	ORCHESTRA_WEB_SEARCH          grant the provider-side web_search tool: "1", or
//	                              {"maxUses":5,"allowedDomains":[...]} (Anthropic only)
//
// If ORCHESTRA_TASK is unset, the task is read from /work/.orchestra/task.md.
// The system prompt is composed as persona + a consistent environment/guidelines
// frame (see internal/prompt); long conversations are auto-summarized to stay
// under the context budget (see internal/agent/compact.go).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"orchestra/agent/internal/agent"
	"orchestra/agent/internal/llm"
	"orchestra/agent/internal/prompt"
	"orchestra/agent/internal/tools"
)

const (
	defaultBaseURL = "http://host.docker.internal:8787/anthropic"
	defaultModel   = "claude-opus-4-8"
	defaultWorkdir = "/work"
	defaultSystem  = "You are a coding agent operating in /work, a git worktree. " +
		"Complete the requested task by reading and editing files with the provided tools. " +
		"Work incrementally, verify your changes, and stop when the task is done."
	// defaultMaxContext caps conversation context before history is summarized.
	// Set ORCHESTRA_MAX_CONTEXT_TOKENS=0 to disable compaction.
	defaultMaxContext = 120000
	// defaultEffort is the reasoning effort used when ORCHESTRA_EFFORT is unset.
	// "medium" sits below the API default ("high") so runs are cost-conscious by
	// default; raise it per-agent when a task needs more depth.
	defaultEffort = "medium"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent: fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	baseURL := envOr("ORCHESTRA_BASE_URL", envOr("ANTHROPIC_BASE_URL", defaultBaseURL))
	model := envOr("ORCHESTRA_MODEL", defaultModel)
	persona := envOr("ORCHESTRA_SYSTEM", defaultSystem)
	workdir := envOr("ORCHESTRA_WORKDIR", defaultWorkdir)
	provider := envOr("ORCHESTRA_PROVIDER", llm.KindAnthropic)
	maxContext := envInt("ORCHESTRA_MAX_CONTEXT_TOKENS", defaultMaxContext)
	keepRecent := envInt("ORCHESTRA_KEEP_RECENT", 0) // 0 => runtime default
	// Cost controls. Effort defaults to "medium" (below the API's "high") to keep
	// runs cost-conscious by default; override per-agent via ORCHESTRA_EFFORT.
	// MaxTokens 0 => the runner's DefaultMaxTokens (16000).
	effort := effortOr(os.Getenv("ORCHESTRA_EFFORT"), defaultEffort)
	maxTokens := envInt("ORCHESTRA_MAX_TOKENS", 0)

	// Compose the system prompt: persona + a consistent environment/guidelines
	// frame. ORCHESTRA_PROMPT_RAW=1 keeps the persona verbatim (power-user opt-out).
	system := persona
	if os.Getenv("ORCHESTRA_PROMPT_RAW") != "1" {
		system = prompt.Build(prompt.Env{
			Persona:    persona,
			Workdir:    workdir,
			Provider:   provider,
			Model:      model,
			Compaction: maxContext > 0,
		})
	}
	gctx := llm.GatewayCtx{
		Session: os.Getenv("ORCHESTRA_SESSION"),
		Run:     os.Getenv("ORCHESTRA_RUN"),
		Stage:   os.Getenv("ORCHESTRA_STAGE"),
	}

	task, err := loadTask(workdir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(task) == "" {
		return fmt.Errorf("no task provided (set ORCHESTRA_TASK or write %s)",
			filepath.Join(workdir, ".orchestra", "task.md"))
	}

	reg := tools.New(workdir)
	// Forbidden-path policy ("path" scope): comma-separated globs (e.g. "*.pem")
	// the file tools must refuse to read/write. Defense-in-depth on top of the
	// read-only rootfs and the /work-only mount.
	if raw := strings.TrimSpace(os.Getenv("ORCHESTRA_DENY_PATHS")); raw != "" {
		var pats []string
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				pats = append(pats, p)
			}
		}
		reg.SetDenyPaths(pats)
	}
	// Runtime delegation (supervisor → sub-agents) is granted by the controller
	// via a depth budget. When depth < max, expose the spawn_subagent tool; it
	// talks to the controller only through the worktree (no network to the host).
	if envInt("ORCHESTRA_DELEGATE_DEPTH", 0) < envInt("ORCHESTRA_DELEGATE_MAX", 0) {
		reg.EnableDelegation()
	}
	// Optional custom tools (HTTP through the gateway), supplied as a JSON array
	// in ORCHESTRA_TOOLS. The agent holds no keys — the gateway injects them.
	if raw := os.Getenv("ORCHESTRA_TOOLS"); raw != "" {
		var defs []tools.HTTPTool
		if err := json.Unmarshal([]byte(raw), &defs); err != nil {
			return fmt.Errorf("parse ORCHESTRA_TOOLS: %w", err)
		}
		reg.SetHTTP(envOr("ORCHESTRA_GATEWAY", strings.TrimSuffix(baseURL, "/anthropic")), gctx, defs)
	}
	// Optional media generation (image / speech / video), supplied as JSON in
	// ORCHESTRA_MEDIA. Same terms as every other call: routed through the
	// gateway, which holds the key. Absent config means absent tools — an agent
	// that was not granted video cannot ask for it.
	if raw := os.Getenv("ORCHESTRA_MEDIA"); raw != "" {
		var cfg tools.MediaConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return fmt.Errorf("parse ORCHESTRA_MEDIA: %w", err)
		}
		reg.SetMedia(envOr("ORCHESTRA_GATEWAY", strings.TrimSuffix(baseURL, "/anthropic")), gctx, cfg)
	}
	// Optional web search. Unlike every other tool, the agent does not run this
	// one — Anthropic performs the search and returns the results in the same
	// response — so it needs no gateway route and gives the container no egress.
	// It exists only in the Anthropic dialect: granting it to an OpenAI or
	// Gemini stage would advertise a tool nothing on either side executes.
	if raw := os.Getenv("ORCHESTRA_WEB_SEARCH"); raw != "" {
		cfg, on, err := parseWebSearch(raw)
		if err != nil {
			return err
		}
		if on && strings.EqualFold(provider, llm.KindAnthropic) {
			reg.SetWebSearch(cfg)
		}
	}

	runner := agent.NewRunner(agent.Config{
		Model:            model,
		System:           system,
		Task:             task,
		MaxTokens:        maxTokens,
		Effort:           effort,
		Provider:         llm.NewProvider(provider, baseURL, gctx, nil),
		Tools:            reg,
		LogW:             os.Stdout,
		MaxContextTokens: maxContext,
		KeepRecent:       keepRecent,
	})

	return runner.Run(context.Background())
}

// loadTask resolves the task prompt: ORCHESTRA_TASK wins; otherwise read the
// task file. A missing task file is not an error (the empty-task check in run
// produces a clearer message).
func loadTask(workdir string) (string, error) {
	if t := os.Getenv("ORCHESTRA_TASK"); t != "" {
		return t, nil
	}
	path := filepath.Join(workdir, ".orchestra", "task.md")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read task file %s: %w", path, err)
	}
	return string(b), nil
}

// parseWebSearch reads the ORCHESTRA_WEB_SEARCH grant. It accepts both a plain
// switch ("1") and the JSON object, because the common case is "let this agent
// search" and requiring `{"maxUses":5}` to say that would be ceremony. The bool
// reports whether search is on; an explicit off is not an error.
func parseWebSearch(raw string) (tools.WebSearchConfig, bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes":
		return tools.WebSearchConfig{}, true, nil
	case "0", "false", "off", "no":
		return tools.WebSearchConfig{}, false, nil
	}
	var cfg tools.WebSearchConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return cfg, false, fmt.Errorf("parse ORCHESTRA_WEB_SEARCH: %w", err)
	}
	return cfg, true, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// effortOr validates a reasoning-effort level, returning fallback when the value
// is empty or not one of the accepted levels (guards against a bad env value
// reaching the API, which would 400).
func effortOr(v, fallback string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return fallback
	}
}

// envInt reads an integer env var, returning fallback when unset or unparseable.
func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}
