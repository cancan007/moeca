// Artifact-producing tools: the half of the HTTP tool mechanism that puts bytes
// on disk instead of text in the model's context.
//
// A tool that answers with information returns its body to the model. A tool
// that MAKES something cannot: a generated image is hundreds of kilobytes of
// base64, far past the 16 KiB the text path keeps, and even intact the model
// could only hand it to write_file, which writes the string rather than the
// bytes it encodes. So the response never reaches the model at all — it is
// decoded here and written into /work, and the model is told only where the
// file went.
//
// Everything that decides HOW to ask — route, method, body shape, where the
// payload sits in the answer — is configuration. That is the difference between
// this and the vendor-shaped generation code it replaces: a provider that
// spells its API differently is a tool definition, not a patch.
package tools

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Generation is slow — an image is seconds, a video is minutes — and its
// payload is large, so artifact tools get their own limits rather than the
// text tools' 30s / 16 KiB.
const (
	artifactCallTimeout = 10 * time.Minute
	maxArtifactBytes    = 256 << 20
	defaultPollEvery    = 5 * time.Second
	defaultPollFor      = 15 * time.Minute
)

// pollEvery is a var only so tests can shorten the wait; production never
// changes it.
var pollEvery = defaultPollEvery

// callArtifactTool performs a tool call whose response becomes a file.
func (r *Registry) callArtifactTool(t HTTPTool, args map[string]any) (string, bool) {
	rel := strings.TrimSpace(str(args, "path"))
	dest, err := r.artifactDest(rel, t.Output.Extensions)
	if err != nil {
		return err.Error(), true
	}

	// Implicit and default arguments. `ext` is derived from the path the model
	// chose, because a provider that needs the output format told to it should
	// not be able to disagree with the file it is being written to.
	args = withDefaults(args, t.Defaults)
	args["ext"] = strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")

	subst := func(s string) string { return substitute(s, args) }
	path := subst(t.Path)
	body, contentType, err := r.buildBody(t, args)
	if err != nil {
		return err.Error(), true
	}
	raw, ct, err := r.artifactDo(methodOr(t.Method, http.MethodPost), path, body, contentType, t)
	if err != nil {
		return err.Error(), true
	}

	if t.Output.Poll != nil {
		if raw, ct, err = r.awaitJob(t, path, raw); err != nil {
			return err.Error(), true
		}
	}

	data, err := decodeArtifact(t.Output, raw, ct)
	if err != nil {
		return err.Error(), true
	}
	return writeArtifact(dest, rel, data)
}

// awaitJob drives create → poll → download for an asynchronous generation.
func (r *Registry) awaitJob(t HTTPTool, path string, created []byte) ([]byte, string, error) {
	p := t.Output.Poll
	last := created
	id, status, err := jobState(p, created)
	if err != nil {
		return nil, "", err
	}
	if id == "" {
		return nil, "", fmt.Errorf("%s: job response carried no id at %q: %s", t.Name, pathOr(p.IDPath, "id"), truncate(string(created), 400))
	}

	every := pollEvery
	if p.EverySec > 0 {
		every = time.Duration(p.EverySec) * time.Second
	}
	limit := defaultPollFor
	if p.ForSec > 0 {
		limit = time.Duration(p.ForSec) * time.Second
	}
	deadline := time.Now().Add(limit)

	for !matches(status, p.Done) {
		if matches(status, p.Fail) {
			return nil, "", fmt.Errorf("%s: job %s reported %q: %s", t.Name, id, status, jobError(p, last))
		}
		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("%s: job %s did not finish within %s (last status %q)", t.Name, id, limit, status)
		}
		time.Sleep(every)
		body, _, err := r.artifactDo(http.MethodGet, path+withID(pathOr(p.StatusURL, "/{{id}}"), id), nil, "", t)
		if err != nil {
			return nil, "", err
		}
		last = body
		if id, status, err = jobState(p, body); err != nil {
			return nil, "", err
		}
	}
	return r.artifactDo(http.MethodGet, path+withID(pathOr(p.ResultURL, "/{{id}}/content"), id), nil, "", t)
}

// jobState reads the id and status out of a job envelope.
func jobState(p *ToolPoll, body []byte) (id, status string, err error) {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", "", fmt.Errorf("job response was not JSON: %s", truncate(string(body), 400))
	}
	id, _ = jsonPath(doc, pathOr(p.IDPath, "id")).(string)
	switch v := jsonPath(doc, pathOr(p.StatusPath, "status")).(type) {
	case string:
		status = v
	case nil:
		status = ""
	default:
		status = fmt.Sprint(v)
	}
	return id, status, nil
}

// decodeArtifact turns the final response into the bytes to write.
func decodeArtifact(out *ToolOutput, raw []byte, contentType string) ([]byte, error) {
	if out.Kind == "binary" {
		// A JSON body where bytes were expected is an error envelope that came
		// back with a 200 — which does happen, and is worth saying plainly
		// rather than writing as if it were an image.
		if strings.Contains(contentType, "json") {
			return nil, fmt.Errorf("expected %s bytes but the provider sent JSON: %s", "artifact", truncate(string(raw), 400))
		}
		return raw, nil
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("expected a JSON response to read %q from: %s", out.JSONPath, truncate(string(raw), 400))
	}
	v := jsonPath(doc, out.JSONPath)
	s, ok := v.(string)
	if !ok || s == "" {
		// The common way for this to be missing is a provider that answered
		// with a link instead of the bytes. Fetching it would mean a second
		// request to a host the gateway has not allowlisted — exactly the
		// egress this design forbids — so say that, rather than reporting a
		// generic missing field and leaving the operator to guess.
		if u := siblingURL(doc, out.JSONPath); u != "" {
			return nil, fmt.Errorf("the provider returned a URL (%s) instead of the bytes; configure it to return the data inline", truncate(u, 200))
		}
		return nil, fmt.Errorf("no value at %q in the response: %s", out.JSONPath, truncate(string(raw), 400))
	}
	// A URL where the payload should be, same reasoning.
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return nil, fmt.Errorf("%q holds a URL, not the data; configure the provider to return the bytes inline", out.JSONPath)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("value at %q was not valid base64: %v", out.JSONPath, err)
	}
	return data, nil
}

// jsonPath walks a dot-separated path, treating numeric segments as array
// indices: "data.0.b64_json".
func jsonPath(doc any, path string) any {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			cur = node[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			cur = node[i]
		default:
			return nil
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

// artifactDest validates the model's chosen output path against the tool's
// allowed extensions and resolves it inside /work.
func (r *Registry) artifactDest(rel string, exts []string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("path is required: say where the file should be written, relative to /work")
	}
	if len(exts) > 0 {
		got := strings.ToLower(filepath.Ext(rel))
		if !matches(got, exts) {
			return "", fmt.Errorf("path %q must end in one of %s", rel, strings.Join(exts, ", "))
		}
	}
	return r.resolve(rel)
}

func (r *Registry) artifactDo(method, path string, body io.Reader, contentType string, t HTTPTool) ([]byte, string, error) {
	if r.gateway == "" {
		return nil, "", fmt.Errorf("http tools are not configured")
	}
	if r.artifactHTTP == nil {
		r.artifactHTTP = &http.Client{Timeout: artifactCallTimeout}
	}
	if method == http.MethodGet {
		body = nil
	}
	req, err := http.NewRequest(method, r.gateway+path, body)
	if err != nil {
		return nil, "", fmt.Errorf("bad tool request: %v", err)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	r.gctx.Apply(req.Header)
	if t.TargetHeader != "" {
		req.Header.Set("X-Orchestra-Target", t.TargetHeader)
	}
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	resp, err := r.artifactHTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("tool request failed: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactBytes))
	if err != nil {
		return nil, "", fmt.Errorf("reading the response failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

// withOutputPath adds the destination-path property to a tool's advertised
// schema and makes it required.
func withOutputPath(schema map[string]any, exts []string) map[string]any {
	out := map[string]any{}
	for k, v := range schema {
		out[k] = v
	}
	props := map[string]any{}
	if p, ok := out["properties"].(map[string]any); ok {
		for k, v := range p {
			props[k] = v
		}
	}
	desc := `Where to write the result, relative to /work, e.g. "artifacts/out.png".`
	if len(exts) > 0 {
		desc += " Must end in " + strings.Join(exts, ", ") + "."
	}
	props["path"] = map[string]any{"type": "string", "description": desc}
	out["properties"] = props

	req := []string{"path"}
	if existing, ok := out["required"].([]string); ok {
		for _, n := range existing {
			if n != "path" {
				req = append(req, n)
			}
		}
	} else if existing, ok := out["required"].([]any); ok {
		for _, n := range existing {
			if s, _ := n.(string); s != "" && s != "path" {
				req = append(req, s)
			}
		}
	}
	out["required"] = req
	return out
}

func matches(got string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimSpace(a), got) {
			return true
		}
	}
	return false
}

func pathOr(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func withID(tmpl, id string) string { return strings.ReplaceAll(tmpl, "{{id}}", id) }

func methodOr(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return strings.ToUpper(v)
	}
	return def
}

// sortedKeys returns a map's keys in a stable order, so a rendered body is
// deterministic (and so a prompt-cache prefix stays stable).
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// quoteJSON renders a string as a JSON scalar. Template placeholders are quoted
// like any other string: they are replaced before the body is parsed.
func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// schema builds the object schema shape the model expects: every property is a
// string, and required names the ones without a default.
func schema(props map[string]string, required ...string) map[string]any {
	p := map[string]any{}
	for name, desc := range props {
		p[name] = map[string]any{"type": "string", "description": desc}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": p, "required": required}
}

// writeArtifact puts the generated bytes on disk and tells the model where they
// went — the response body itself never reaches the model.
func writeArtifact(dest, rel string, data []byte) (string, bool) {
	if len(data) == 0 {
		return "the provider returned no bytes", true
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "could not create directory: " + err.Error(), true
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "could not write file: " + err.Error(), true
	}
	return fmt.Sprintf("wrote %s (%d bytes)", rel, len(data)), false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// withDefaults returns args with any missing (or blank) parameter filled from
// the tool's defaults. The model's own argument always wins.
func withDefaults(args map[string]any, defaults map[string]string) map[string]any {
	out := map[string]any{}
	for k, v := range args {
		out[k] = v
	}
	for k, v := range defaults {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if s, ok := out[k]; !ok || strings.TrimSpace(valueString(s)) == "" {
			out[k] = v
		}
	}
	return out
}

// jobError extracts a failed job's own explanation, which is the only part of a
// refusal worth reporting.
func jobError(p *ToolPoll, body []byte) string {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return truncate(string(body), 300)
	}
	if v := jsonPath(doc, pathOr(p.ErrorPath, "error")); v != nil {
		return truncate(fmt.Sprint(v), 300)
	}
	return truncate(string(body), 300)
}

// siblingURL looks for a "url" next to where the payload was expected, so the
// most common misconfiguration can be named instead of merely detected.
func siblingURL(doc any, jsonPath_ string) string {
	segs := strings.Split(jsonPath_, ".")
	if len(segs) == 0 {
		return ""
	}
	parent := jsonPath(doc, strings.Join(segs[:len(segs)-1], "."))
	obj, ok := parent.(map[string]any)
	if !ok {
		return ""
	}
	u, _ := obj["url"].(string)
	return u
}
