package tools

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
)

// An artifact tool is the generic form of what image/speech/video generation
// needs. These pin the parts that let a provider with a different API shape be
// a tool definition rather than a code change.

func artifactRegistry(t *testing.T, gateway string, def HTTPTool) (*Registry, string) {
	t.Helper()
	work := t.TempDir()
	r := New(work)
	r.SetHTTP(gateway, llm.GatewayCtx{Session: "s"}, []HTTPTool{def})
	return r, work
}

// The payload can live anywhere in the response — this is the whole point of
// the JSON path being configuration instead of a hardcoded `data[0].b64_json`.
func TestArtifactToolReadsBase64FromAnyJSONPath(t *testing.T) {
	png := []byte("\x89PNGfake")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"predictions": []map[string]string{{"bytesBase64Encoded": base64.StdEncoding.EncodeToString(png)}},
		})
	}))
	defer srv.Close()

	r, work := artifactRegistry(t, srv.URL, HTTPTool{
		Name: "imagen", Method: "POST", Path: "/vertex/predict",
		Body:   `{"instances":[{"prompt":"{{prompt}}"}]}`,
		Output: &ToolOutput{Kind: "base64", JSONPath: "predictions.0.bytesBase64Encoded", Extensions: []string{".png"}},
	})

	out, isErr := r.Dispatch("imagen", map[string]any{"prompt": "a dog", "path": "art/dog.png"})
	if isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(work, "art", "dog.png"))
	if err != nil {
		t.Fatalf("artifact not written: %v", err)
	}
	if string(got) != string(png) {
		t.Errorf("wrote %q, want the decoded bytes", got)
	}
	// The response never reaches the model; it is told where the file went.
	if strings.Contains(out, "bytesBase64Encoded") {
		t.Errorf("the encoded payload leaked into the model's context: %s", out)
	}
}

// The destination path is added to the advertised schema by the engine, so a
// tool definition cannot forget it — and it is required.
func TestArtifactToolAdvertisesAPathParameter(t *testing.T) {
	r, _ := artifactRegistry(t, "http://gw", HTTPTool{
		Name: "gen", Path: "/x",
		InputSchema: map[string]any{"type": "object",
			"properties": map[string]any{"prompt": map[string]any{"type": "string"}},
			"required":   []string{"prompt"}},
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".mp3"}},
	})
	var def llm.Tool
	for _, d := range r.Definitions() {
		if d.Name == "gen" {
			def = d
		}
	}
	props, _ := def.InputSchema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Fatalf("no path property in %v", def.InputSchema)
	}
	req, _ := def.InputSchema["required"].([]string)
	if !matches("path", req) || !matches("prompt", req) {
		t.Errorf("required = %v, want both path and the author's own prompt", req)
	}
	if desc, _ := props["path"].(map[string]any)["description"].(string); !strings.Contains(desc, ".mp3") {
		t.Errorf("path description does not mention the allowed extension: %q", desc)
	}
}

// The extension whitelist is what stops a "generated file" from landing as a
// script; it must be enforced before any request is made.
func TestArtifactToolRefusesAWrongExtensionWithoutCallingOut(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true }))
	defer srv.Close()

	r, _ := artifactRegistry(t, srv.URL, HTTPTool{
		Name: "gen", Path: "/x", Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	out, isErr := r.Dispatch("gen", map[string]any{"path": "payload.sh"})
	if !isErr || !strings.Contains(out, ".png") {
		t.Errorf("expected a refusal naming the allowed extension, got %q", out)
	}
	if called {
		t.Error("the provider was called before the destination was validated")
	}
}

// An optional parameter the model omits must be dropped from the request, not
// sent as the literal "{{size}}" — which providers reject, and which was the
// flaw in every hand-authored generation tool.
func TestUnfilledOptionalParametersAreNotSent(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		json.NewDecoder(req.Body).Decode(&got)
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	r, _ := artifactRegistry(t, srv.URL, HTTPTool{
		Name: "gen", Path: "/x",
		Body:   `{"prompt":"{{prompt}}","size":"{{size}}","quality":"{{quality}}"}`,
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	if out, isErr := r.Dispatch("gen", map[string]any{"prompt": "p", "path": "a.png"}); isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	if _, present := got["size"]; present {
		t.Errorf("size was sent as %v; an omitted optional must not be sent at all", got["size"])
	}
	if _, present := got["quality"]; present {
		t.Errorf("quality was sent as %v", got["quality"])
	}
	if got["prompt"] != "p" {
		t.Errorf("prompt = %v", got["prompt"])
	}
}

// A default fills an omitted parameter; the model's own argument still wins.
func TestDefaultsFillOmittedParameters(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		json.NewDecoder(req.Body).Decode(&got)
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write([]byte("audio"))
	}))
	defer srv.Close()

	def := HTTPTool{
		Name: "say", Path: "/x",
		Defaults: map[string]string{"voice": "alloy"},
		Body:     `{"voice":"{{voice}}","format":"{{ext}}"}`,
		Output:   &ToolOutput{Kind: "binary", Extensions: []string{".mp3", ".wav"}},
	}
	r, _ := artifactRegistry(t, srv.URL, def)
	r.Dispatch("say", map[string]any{"path": "a.wav"})
	if got["voice"] != "alloy" {
		t.Errorf("voice = %v, want the default", got["voice"])
	}
	// ext is derived from the destination, so the provider cannot be told a
	// format that disagrees with the file being written.
	if got["format"] != "wav" {
		t.Errorf("format = %v, want wav from the path", got["format"])
	}

	r2, _ := artifactRegistry(t, srv.URL, def)
	r2.Dispatch("say", map[string]any{"path": "a.mp3", "voice": "echo"})
	if got["voice"] != "echo" {
		t.Errorf("voice = %v, want the model's own argument to win", got["voice"])
	}
}

// A text tool is unchanged: its body still comes back to the model.
func TestTextToolsStillReturnTheirBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r, _ := artifactRegistry(t, srv.URL, HTTPTool{Name: "ping", Path: "/x"})
	out, isErr := r.Dispatch("ping", nil)
	if isErr || !strings.Contains(out, `{"ok":true}`) {
		t.Errorf("text tool = %q (isErr=%v), want the body returned", out, isErr)
	}
}
