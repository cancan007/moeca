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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"orchestra/agent/internal/handoff"
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
	// Defaults fill in optional parameters the model left out, so a template
	// author can say "voice defaults to alloy" without the model having to know
	// it. A parameter with neither an argument nor a default is dropped from a
	// JSON body rather than sent empty (see pruneBody).
	Defaults map[string]string `json:"defaults"`
	// Inputs declare which parameters name a file in /work rather than carrying
	// a value, and how each is sent (a form part, or base64 inside the body).
	// Absent => the tool sends no files, which is every tool that predates this.
	Inputs map[string]ToolInput `json:"inputs"`
	// Output decides what the response becomes. Absent (or "text") returns the
	// body to the model, which is right for an API whose answer is information.
	// An artifact output writes the bytes into /work instead — see ToolOutput.
	Output *ToolOutput `json:"output"`
}

// ToolOutput turns a tool from something that tells the model a fact into
// something that produces a file.
//
// This is what image, speech and video generation actually need, and the only
// thing a generic HTTP tool was missing: everything else — the gateway route,
// the injected key, the per-agent grant, the parameter templating — was already
// here. Without it, generation had to be a second, parallel mechanism hardcoded
// to one vendor's request and response shapes; with it, a provider that spells
// its API differently is a tool definition rather than a patch.
type ToolOutput struct {
	// Kind: "" / "text" (return the body to the model), "binary" (the body IS
	// the artifact) or "base64" (the artifact is base64 at JSONPath).
	Kind string `json:"kind"`
	// JSONPath locates the payload in a JSON response, dot-separated with
	// numeric segments indexing arrays — e.g. "data.0.b64_json".
	JSONPath string `json:"jsonPath"`
	// Extensions the model's chosen output path must end in. A generated file
	// that lands as `.sh` is not an artifact, so the tool refuses rather than
	// trusting the extension the model picked.
	Extensions []string `json:"extensions"`
	// Poll describes an asynchronous job: create, wait, then fetch. Absent for
	// the synchronous case.
	Poll *ToolPoll `json:"poll"`
}

// ToolPoll configures the create → poll → download shape that long generations
// (video, above all) use everywhere. The tool call is held open across the wait
// rather than handing the model a job id it would have to remember to check.
type ToolPoll struct {
	IDPath     string   `json:"idPath"`     // where the job id is, default "id"
	StatusPath string   `json:"statusPath"` // where the status is, default "status"
	Done       []string `json:"done"`       // statuses meaning finished
	Fail       []string `json:"fail"`       // statuses meaning gave up
	// StatusURL and ResultURL are appended to the tool's path, with {{id}}
	// substituted. Defaults "/{{id}}" and "/{{id}}/content".
	StatusURL string `json:"statusUrl"`
	ResultURL string `json:"resultUrl"`
	// ErrorPath is where a failed job explains itself, default "error". The
	// provider's own reason ("content policy") is the only useful thing about a
	// refusal, so it is carried through rather than reduced to a status word.
	ErrorPath string `json:"errorPath"`
	EverySec  int    `json:"everySec"` // poll interval, default 5
	ForSec    int    `json:"forSec"`   // give up after, default 900
}

// producesArtifact reports whether this tool writes a file rather than
// answering the model in text.
func (t HTTPTool) producesArtifact() bool {
	return t.Output != nil && (t.Output.Kind == "binary" || t.Output.Kind == "base64")
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

	// artifactHTTP is the client for tools that produce files. Generation is
	// slow and its payload is large, so it gets its own timeout and read limit
	// rather than the text tools' 30s / 16 KiB. See artifact.go.
	artifactHTTP *http.Client

	// webSearch enables the provider-executed web_search tool via SetWebSearch.
	// It has no client here — nothing in this process performs the search. See
	// websearch.go.
	webSearch *WebSearchConfig

	// produced are the paths written so far, for the stage manifest. Guarded
	// because delegation runs sub-agent tools concurrently with the parent's.
	mu       sync.Mutex
	produced []string
	// attachments are images a tool call read for the model to look at. They
	// wait here because a tool result is a string; the loop collects them and
	// puts them in the same user turn as the results. See view.go.
	attachments []attachment
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
			Description: "Read the full contents of a text file within the /work worktree. Binary files (images, audio, video) and files over 256 KB are refused — the tool reports what the file is instead, which is what a generated artifact needs checking for.",
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
	defs = append(defs, viewImageDef(), viewVideoDef())
	if r.delegate {
		defs = append(defs, spawnSubagentDef())
	}
	defs = append(defs, r.webSearchDefinition()...)
	for _, t := range r.httpTools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		// An artifact tool always takes a destination path, whatever its author
		// wrote in the schema. The model has to say where the file goes — that
		// is what makes the result reviewable — so this is added here rather
		// than left to every tool definition to remember.
		if t.producesArtifact() {
			schema = withOutputPath(schema, t.Output.Extensions)
		}
		defs = append(defs, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

// writerTools are the tools whose success means a file now exists at
// args["path"]. Recording the path here rather than inside each writer keeps
// one list to keep current, and covers the media tools, which write through a
// package-level helper.
var writerTools = map[string]bool{
	"write_file":      true,
	"edit_file":       true,
	"generate_image":  true,
	"generate_speech": true,
	"generate_video":  true,
}

// Dispatch executes the named tool with args (already-decoded JSON input) and
// returns a result string plus an isError flag for the tool_result block. An
// unknown tool name or a tool error yields (message, true) rather than a Go
// error, so the loop can feed the failure back to the model.
func (r *Registry) Dispatch(name string, args map[string]any) (string, bool) {
	out, isErr := r.dispatch(name, args)
	// What the stage produced is part of what it hands downstream, and the model
	// cannot be relied on to report it — the run this was built for ended with
	// three agents each certain the others had left them something.
	if !isErr && writerTools[name] {
		if p := strings.TrimSpace(str(args, "path")); p != "" {
			r.mu.Lock()
			r.produced = append(r.produced, p)
			r.mu.Unlock()
		}
	}
	return out, isErr
}

// Produced lists the worktree-relative paths this run wrote, deduped and
// sorted.
func (r *Registry) Produced() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return handoff.SortedUnique(r.produced)
}

func (r *Registry) dispatch(name string, args map[string]any) (string, bool) {
	switch name {
	case "list_files":
		return r.listFiles(str(args, "subdir"))
	case "read_file":
		return r.readFile(str(args, "path"))
	case "view_image":
		return r.viewImage(str(args, "path"))
	case "view_video":
		return r.viewVideo(str(args, "path"), str(args, "frames"))
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
		if t, ok := r.httpTools[name]; ok {
			if t.producesArtifact() {
				return r.callArtifactTool(t, args)
			}
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
	// Files a text tool sends travel the same way an artifact tool's do: this is
	// how "upload it" is expressed, whatever the response then turns out to be.
	body, contentType, err := r.buildBody(t, args)
	if err != nil {
		return err.Error(), true
	}
	req, err := http.NewRequest(method, r.gateway+subst(t.Path), body)
	if err != nil {
		return "bad tool request: " + err.Error(), true
	}
	req.Header.Set("Content-Type", contentType)
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

// unfilled matches a {{placeholder}} the model supplied no argument for.
var unfilled = regexp.MustCompile(`^\{\{[A-Za-z0-9_.-]+\}\}$`)

// pruneBody drops the members of a JSON object body that the model left
// unfilled, and returns the body unchanged if it is not a JSON object.
//
// Substitution only replaces the parameters that were actually supplied, so an
// optional one the model omitted survives into the request as the literal text
// "{{size}}" — which providers reject, or worse, accept as a nonsense value.
// An omitted optional parameter has to mean "do not send this field", which is
// what every one of these APIs expects.
func pruneBody(body string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return body // not a JSON object: a template we have no business editing
	}
	changed := false
	for k, v := range doc {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s == "" || unfilled.MatchString(s) {
			delete(doc, k)
			changed = true
		}
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return string(out)
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

// maxReadBytes bounds what read_file will put into the model's context. Source
// files and reports sit far below it; anything above is being read for a reason
// the tool cannot serve.
const maxReadBytes = 256 << 10

// readFile returns a file's contents as text, and refuses when it is not text.
//
// It used to return whatever bytes were on disk. That was survivable while
// agents only wrote source files, and stopped being survivable the moment they
// could generate images: an integrator stage read the 1024x1024 PNG a worker
// had just produced, the conversation reached 1.6 million tokens, and the run
// died on `prompt is too long` — after the artifact it was verifying had been
// created successfully. The file was fine; reading it was the failure.
//
// Both refusals describe the file instead, because "there is a 1.2 MB PNG at
// this path" is the answer the caller actually wanted.
func (r *Registry) readFile(path string) (string, bool) {
	abs, err := r.resolve(path)
	if err != nil {
		return err.Error(), true
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Sprintf("read_file failed: %v", err), true
	}
	if info.IsDir() {
		return fmt.Sprintf("read_file failed: %s is a directory", path), true
	}
	if info.Size() > maxReadBytes {
		return fmt.Sprintf("%s is %s, over the %s read limit — it exists, but it is too large to read into context. Use list_files to confirm it, or a command stage to process it.",
			path, byteSize(info.Size()), byteSize(maxReadBytes)), true
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Sprintf("read_file failed: %v", err), true
	}
	if !isText(b) {
		return fmt.Sprintf("%s is a binary file (%s) — it exists and is not readable as text. For a generated artifact, that it was written is the thing to check.",
			path, byteSize(info.Size())), true
	}
	return string(b), false
}

// isText reports whether a file's bytes can be handed to a model as text. A NUL
// byte never occurs in text and is what every format from PNG to zip carries in
// its first bytes; invalid UTF-8 catches the rest.
func isText(b []byte) bool {
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	return utf8.Valid(head)
}

// byteSize renders a size the way an error message should read.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
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
