// Sending a file the run already has.
//
// An artifact tool could make a picture but never start from one: the body it
// posts is JSON built from the model's arguments, so there was no way to say
// "and this file from /work goes with it". That ruled out every edit route
// there is — img2img, a video generated from a reference frame, "same dog,
// different background" — and it ruled them out for every provider at once.
//
// Two shapes cover the field, because the providers genuinely differ:
//
//	multipart — the file is a form part (OpenAI's edit routes work this way)
//	base64    — the file is a string inside the JSON body (Imagen and friends)
//
// Both are declared per parameter in the tool definition, which is the point:
// which shape a provider wants is configuration, exactly as the response
// binding is. Nothing here is specific to a vendor.
package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"sort"
	"strings"
)

// ToolInput declares that a parameter names a file in /work rather than
// carrying a value, and how that file should be sent.
type ToolInput struct {
	// As is "multipart" or "base64".
	As string `json:"as"`
	// Field is the multipart form field name. Empty => the parameter's own name.
	Field string `json:"field"`
	// MaxBytes caps this input; 0 => defaultInputMax.
	MaxBytes int `json:"maxBytes"`
}

// defaultInputMax bounds an upload. Generous enough for a generated image or a
// short clip, small enough that a mistake is not a multi-hundred-megabyte POST.
const defaultInputMax = 32 << 20

const (
	inputMultipart = "multipart"
	inputBase64    = "base64"
)

// hasFileInputs reports whether this tool sends any file.
func (t HTTPTool) hasFileInputs() bool { return len(t.Inputs) > 0 }

// wantsMultipart reports whether any declared input must be sent as a form part.
func (t HTTPTool) wantsMultipart() bool {
	for _, in := range t.Inputs {
		if in.As == inputMultipart {
			return true
		}
	}
	return false
}

// readInputs loads the files a call refers to, keyed by parameter name.
//
// The paths come from the model, so they go through the same guard as every
// other file tool: a parameter that names a file is still a path an agent
// chose.
func (r *Registry) readInputs(t HTTPTool, args map[string]any) (map[string][]byte, error) {
	if !t.hasFileInputs() {
		return nil, nil
	}
	out := map[string][]byte{}
	for name, in := range t.Inputs {
		rel := strings.TrimSpace(str(args, name))
		if rel == "" {
			continue // an optional input the model did not supply
		}
		abs, err := r.resolve(rel)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		max := in.MaxBytes
		if max <= 0 {
			max = defaultInputMax
		}
		if info.Size() > int64(max) {
			return nil, fmt.Errorf("%s is %s, over the %s limit for an upload", rel, byteSize(info.Size()), byteSize(int64(max)))
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if len(b) == 0 {
			return nil, fmt.Errorf("%s is empty", rel)
		}
		out[name] = b
	}
	return out, nil
}

// buildBody renders the request body for a call, and the Content-Type that
// describes it.
//
// With no file inputs this is what it always was: the JSON template, with the
// model's arguments substituted and the unfilled ones pruned.
func (r *Registry) buildBody(t HTTPTool, args map[string]any) (io.Reader, string, error) {
	files, err := r.readInputs(t, args)
	if err != nil {
		return nil, "", err
	}

	// base64 inputs need no new machinery: the file becomes the argument's
	// value, and the existing substitution puts it wherever the template says.
	subArgs := args
	if len(files) > 0 {
		subArgs = map[string]any{}
		for k, v := range args {
			subArgs[k] = v
		}
		for name, b := range files {
			if t.Inputs[name].As == inputBase64 {
				subArgs[name] = b64(b)
			}
		}
	}
	body := pruneBody(substitute(t.Body, subArgs))

	if !t.wantsMultipart() || len(files) == 0 {
		if strings.TrimSpace(body) == "" {
			return nil, "application/json", nil
		}
		return strings.NewReader(body), "application/json", nil
	}
	return buildMultipart(t, body, files)
}

// buildMultipart turns the JSON body template into form fields and adds the
// files as parts.
//
// The template is reused rather than replaced by a second authoring surface: a
// tool that posts `{"model":"gpt-image-1","prompt":"{{prompt}}"}` should say the
// same thing whether or not it also uploads a picture, and the author should
// not have to write it twice.
func buildMultipart(t HTTPTool, body string, files map[string][]byte) (io.Reader, string, error) {
	fields := map[string]any{}
	if strings.TrimSpace(body) != "" {
		if err := json.Unmarshal([]byte(body), &fields); err != nil {
			return nil, "", fmt.Errorf("a multipart tool's body must be a flat JSON object of form fields: %v", err)
		}
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, k := range sortedAnyKeys(fields) {
		// Nested values have no form representation; a provider that wants one
		// would take JSON, not multipart.
		switch v := fields[k].(type) {
		case string:
			_ = w.WriteField(k, v)
		case float64, bool:
			_ = w.WriteField(k, fmt.Sprint(v))
		}
	}
	for _, name := range sortedByteKeys(files) {
		in := t.Inputs[name]
		if in.As != inputMultipart {
			continue // already embedded in the fields above
		}
		field := in.Field
		if field == "" {
			field = name
		}
		part, err := w.CreateFormFile(field, name)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(files[name]); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// Both key helpers sort, so a request is byte-identical for identical inputs —
// the same reason the tool definitions are sorted before they are advertised.
func sortedAnyKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedByteKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
