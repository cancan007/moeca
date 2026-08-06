// Package tools implements the file-editing tools the agent exposes to Claude,
// all scoped to the mounted git worktree (/work).
//
// Every tool resolves its path argument against the workdir root and rejects
// anything that is absolute or escapes the root (the same path-guard idea as
// hostagent/internal/git's FileContent: clean the path, reject leading `/`
// or a `..` prefix). The container mounts ONLY /work, but defence-in-depth is
// cheap and keeps a buggy or adversarial model from touching the tmpfs.
//
// The Registry both advertises the tool definitions (for the Messages API
// `tools` field) and dispatches a call by name, returning a string result and
// an isError flag suitable for a tool_result content block.
package tools

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"orchestra/agent/internal/llm"
)

// HTTPTool is a user-defined tool that performs an HTTP call THROUGH the gateway
// (never directly). Path/Headers/Body/TargetHeader are templates in which
// {{param}} is replaced by the model-supplied argument. Because every request
// goes through the gateway, its allowlist / SSRF-deny / write-authz / key
// injection apply regardless of what the model puts in the parameters — so a
// custom tool cannot widen the sandbox's reach.
type HTTPTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	InputSchema  map[string]any    `json:"inputSchema"`
	Method       string            `json:"method"`
	Path         string            `json:"path"` // gateway-relative, e.g. "/slack/chat.postMessage"
	Headers      map[string]string `json:"headers"`
	Body         string            `json:"body"`
	TargetHeader string            `json:"targetHeader"` // for /fetch dynamic targets
}

// Registry dispatches tool calls against a fixed workdir root, plus any
// gateway-routed HTTP tools registered via SetHTTP.
type Registry struct {
	root string

	gateway   string // gateway origin, e.g. http://orchestra-gateway:8787
	gctx      llm.GatewayCtx
	http      *http.Client
	httpTools map[string]HTTPTool

	// delegation (spawn_subagent): file-based, no network. Enabled via
	// EnableDelegation when the controller grants a delegation budget.
	delegate        bool
	delegateTimeout time.Duration
	delegatePoll    time.Duration

	// denyPaths are forbidden-path globs (policy "path" scope) enforced on every
	// file tool via resolve.
	denyPaths []string

	// media generation (generate_image / generate_speech / generate_video),
	// enabled per agent template via SetMedia. See media.go.
	media     *MediaConfig
	mediaHTTP *http.Client

	// webSearch enables the provider-executed web_search tool via SetWebSearch.
	// It has no client here — nothing in this process performs the search. See
	// websearch.go.
	webSearch *WebSearchConfig
}

// New returns a Registry rooted at workdir (typically /work).
func New(workdir string) *Registry {
	return &Registry{root: workdir, httpTools: map[string]HTTPTool{}}
}

// SetHTTP registers gateway-routed custom tools. gateway is the gateway origin;
// gctx carries the session + run/stage attribution sent on every tool call.
func (r *Registry) SetHTTP(gateway string, gctx llm.GatewayCtx, defs []HTTPTool) {
	r.gateway = strings.TrimRight(gateway, "/")
	r.gctx = gctx
	r.http = &http.Client{Timeout: 30 * time.Second}
	for _, d := range defs {
		if d.Name != "" {
			r.httpTools[d.Name] = d
		}
	}
}

// resolve maps a tool-supplied relative path to an absolute path inside root,
// rejecting absolute paths and any that escape the root via `..`.
func (r *Registry) resolve(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q: must be relative to /work and must not escape it", rel)
	}
	if pat, denied := r.pathDenied(clean); denied {
		return "", fmt.Errorf("path %q is blocked by policy (%s)", rel, pat)
	}
	return filepath.Join(r.root, clean), nil
}

// gitDirRefused is the reason returned for any path under (or equal to) .git.
//
// The worktree's `.git` names the git dir that host-side git resolves hooks
// from, so writing it lets an agent point host git at a directory it controls.
// The host neutralises hooks on its own git calls, but this is the other half:
// nothing an agent legitimately needs requires writing git's own metadata, and
// leaving it to configurable policy means a trimmed denyPaths list silently
// re-opens the door.
const gitDirRefused = ".git (git metadata is never writable)"

// pathDenied reports whether a cleaned relative path is refused — either as
// git metadata (always) or by a forbidden-path pattern from the policy's "path"
// scope (e.g. "*.pem", "secrets/*"). Patterns without a slash match the
// basename; patterns with a slash match the full path.
func (r *Registry) pathDenied(clean string) (string, bool) {
	slashed := filepath.ToSlash(clean)
	if slashed == ".git" || strings.HasPrefix(slashed, ".git/") {
		return gitDirRefused, true
	}
	base := filepath.Base(clean)
	for _, pat := range r.denyPaths {
		target := base
		if strings.Contains(pat, "/") {
			target = filepath.ToSlash(clean)
		}
		if ok, _ := path.Match(pat, target); ok {
			return pat, true
		}
	}
	return "", false
}

// SetDenyPaths installs forbidden-path glob patterns (policy "path" scope),
// enforced on every file tool via resolve.
func (r *Registry) SetDenyPaths(pats []string) { r.denyPaths = pats }

// Definitions returns the tool schemas to advertise to the model, sorted by
// name for deterministic ordering (stable prompt-cache prefix).
func (r *Registry) Definitions() []llm.Tool {
	defs := []llm.Tool{
		{
			Name:        "list_files",
			Description: "List files under the /work git worktree, recursively. Optionally restrict to a subdirectory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subdir": map[string]any{
						"type":        "string",
						"description": "Optional subdirectory (relative to /work) to list. Omit to list everything.",
					},
				},
			},
		},
		{
			Name:        "read_file",
			Description: "Read the full contents of a file within the /work worktree.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to /work.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Create or overwrite a file within the /work worktree with the given content. Parent directories are created as needed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to /work.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Full file content to write.",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "edit_file",
			Description: "Replace an exact, unique string in a file within /work. Errors if old_str does not appear exactly once.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path relative to /work.",
					},
					"old_str": map[string]any{
						"type":        "string",
						"description": "Exact string to find. Must occur exactly once in the file.",
					},
					"new_str": map[string]any{
						"type":        "string",
						"description": "Replacement string.",
					},
				},
				"required": []string{"path", "old_str", "new_str"},
			},
		},
	}
	if r.delegate {
		defs = append(defs, spawnSubagentDef())
	}
	defs = append(defs, r.mediaDefinitions()...)
	defs = append(defs, r.webSearchDefinition()...)
	for _, t := range r.httpTools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		defs = append(defs, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// Dispatch executes the named tool with args (already-decoded JSON input) and
// returns a result string plus an isError flag for the tool_result block. An
// unknown tool name or a tool error yields (message, true) rather than a Go
// error, so the loop can feed the failure back to the model.
func (r *Registry) Dispatch(name string, args map[string]any) (string, bool) {
	switch name {
	case "list_files":
		return r.listFiles(str(args, "subdir"))
	case "read_file":
		return r.readFile(str(args, "path"))
	case "write_file":
		return r.writeFile(str(args, "path"), str(args, "content"))
	case "edit_file":
		return r.editFile(str(args, "path"), str(args, "old_str"), str(args, "new_str"))
	case "spawn_subagent":
		if !r.delegate {
			return "spawn_subagent is not enabled for this agent", true
		}
		return r.spawnSubagent(str(args, "role"), str(args, "task"), str(args, "model"))
	case WebSearchToolName:
		if r.webSearch == nil {
			return fmt.Sprintf("unknown tool %q", name), true
		}
		return webSearchMisdispatch, true
	default:
		if out, isErr, handled := r.dispatchMedia(name, args); handled {
			return out, isErr
		}
		if t, ok := r.httpTools[name]; ok {
			return r.callHTTP(t, args)
		}
		return fmt.Sprintf("unknown tool %q", name), true
	}
}

// callHTTP executes a custom HTTP tool through the gateway. Template params are
// substituted from args; every request carries the gateway session. The response
// body (status-prefixed, size-capped) is returned to the model; only a transport
// failure is an isError.
func (r *Registry) callHTTP(t HTTPTool, args map[string]any) (string, bool) {
	if r.http == nil || r.gateway == "" {
		return "http tools are not configured", true
	}
	subst := func(s string) string { return substitute(s, args) }
	method := strings.ToUpper(t.Method)
	if method == "" {
		method = http.MethodPost
	}
	var body io.Reader
	if t.Body != "" {
		body = strings.NewReader(subst(t.Body))
	}
	req, err := http.NewRequest(method, r.gateway+subst(t.Path), body)
	if err != nil {
		return "bad tool request: " + err.Error(), true
	}
	req.Header.Set("Content-Type", "application/json")
	r.gctx.Apply(req.Header)
	if t.TargetHeader != "" {
		req.Header.Set("X-Orchestra-Target", subst(t.TargetHeader))
	}
	for k, v := range t.Headers {
		req.Header.Set(k, subst(v))
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return "tool request failed: " + err.Error(), true
	}
	defer resp.Body.Close()
	const maxBody = 16 << 10
	out, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(out)), false
}

// substitute replaces {{key}} with the string form of args[key].
func substitute(s string, args map[string]any) string {
	for k, v := range args {
		s = strings.ReplaceAll(s, "{{"+k+"}}", valueString(v))
	}
	return s
}

func valueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func (r *Registry) listFiles(subdir string) (string, bool) {
	start := r.root
	if subdir != "" {
		resolved, err := r.resolve(subdir)
		if err != nil {
			return err.Error(), true
		}
		start = resolved
	}
	var files []string
	err := filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the .git directory — it's noise for a coding task.
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(r.root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return fmt.Sprintf("list_files failed: %v", err), true
	}
	sort.Strings(files)
	if len(files) == 0 {
		return "(no files)", false
	}
	return strings.Join(files, "\n"), false
}

func (r *Registry) readFile(path string) (string, bool) {
	abs, err := r.resolve(path)
	if err != nil {
		return err.Error(), true
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Sprintf("read_file failed: %v", err), true
	}
	return string(b), false
}

func (r *Registry) writeFile(path, content string) (string, bool) {
	abs, err := r.resolve(path)
	if err != nil {
		return err.Error(), true
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Sprintf("write_file failed: %v", err), true
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("write_file failed: %v", err), true
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), false
}

func (r *Registry) editFile(path, oldStr, newStr string) (string, bool) {
	if oldStr == "" {
		return "edit_file failed: old_str must not be empty", true
	}
	abs, err := r.resolve(path)
	if err != nil {
		return err.Error(), true
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Sprintf("edit_file failed: %v", err), true
	}
	content := string(b)
	n := strings.Count(content, oldStr)
	switch {
	case n == 0:
		return "edit_file failed: old_str not found in file", true
	case n > 1:
		return fmt.Sprintf("edit_file failed: old_str found %d times, must be unique", n), true
	}
	updated := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return fmt.Sprintf("edit_file failed: %v", err), true
	}
	return fmt.Sprintf("edited %s", path), false
}

// str extracts a string field from decoded JSON args, tolerating absence.
func str(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}
