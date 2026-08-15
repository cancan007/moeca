package tools

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
)

// An artifact tool could make a picture but never start from one. These pin the
// two shapes providers actually use, so that "edit this image" is a tool
// definition rather than a patch.

func toolWith(t *testing.T, gateway string, def HTTPTool) (*Registry, string) {
	t.Helper()
	work := t.TempDir()
	r := New(work)
	r.SetHTTP(gateway, llm.GatewayCtx{Session: "s"}, []HTTPTool{def})
	return r, work
}

func TestFileInputIsSentAsAMultipartPart(t *testing.T) {
	var gotFields map[string]string
	var gotFile []byte
	var gotFieldName, gotFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("content type = %q", req.Header.Get("Content-Type"))
		}
		mr := multipart.NewReader(req.Body, params["boundary"])
		gotFields = map[string]string{}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				gotFieldName, gotFilename, gotFile = p.FormName(), p.FileName(), b
			} else {
				gotFields[p.FormName()] = string(b)
			}
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("edited-bytes"))
	}))
	defer srv.Close()

	r, work := toolWith(t, srv.URL, HTTPTool{
		Name: "edit_image", Method: "POST", Path: "/openai/v1/images/edits",
		Body:   `{"model":"gpt-image-1","prompt":"{{prompt}}"}`,
		Inputs: map[string]ToolInput{"image": {As: "multipart", Field: "image"}},
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	os.WriteFile(filepath.Join(work, "dog.png"), []byte("\x89PNGoriginal"), 0o644)

	out, isErr := r.Dispatch("edit_image", map[string]any{
		"prompt": "same dog, night sky", "image": "dog.png", "path": "out/edited.png",
	})
	if isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	if string(gotFile) != "\x89PNGoriginal" {
		t.Errorf("uploaded bytes = %q", gotFile)
	}
	if gotFieldName != "image" || gotFilename == "" {
		t.Errorf("form file field = %q, filename = %q", gotFieldName, gotFilename)
	}
	// The JSON body template doubles as the form fields, so a tool says the same
	// thing whether or not it uploads.
	if gotFields["model"] != "gpt-image-1" || gotFields["prompt"] != "same dog, night sky" {
		t.Errorf("form fields = %v", gotFields)
	}
	// And the result still lands as a file.
	if b, err := os.ReadFile(filepath.Join(work, "out", "edited.png")); err != nil || string(b) != "edited-bytes" {
		t.Errorf("artifact = %q (%v)", b, err)
	}
}

func TestFileInputCanBeBase64InTheJSONBody(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if ct := req.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type = %q, want application/json", ct)
		}
		json.NewDecoder(req.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predictions":[{"bytesBase64Encoded":"` + base64.StdEncoding.EncodeToString([]byte("new")) + `"}]}`))
	}))
	defer srv.Close()

	r, work := toolWith(t, srv.URL, HTTPTool{
		Name: "imagen_edit", Method: "POST", Path: "/vertex/predict",
		Body:   `{"instances":[{"prompt":"{{prompt}}","image":{"bytesBase64Encoded":"{{image}}"}}]}`,
		Inputs: map[string]ToolInput{"image": {As: "base64"}},
		Output: &ToolOutput{Kind: "base64", JSONPath: "predictions.0.bytesBase64Encoded", Extensions: []string{".png"}},
	})
	os.WriteFile(filepath.Join(work, "in.png"), []byte("SRC"), 0o644)

	if out, isErr := r.Dispatch("imagen_edit", map[string]any{"prompt": "p", "image": "in.png", "path": "o.png"}); isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	inst := body["instances"].([]any)[0].(map[string]any)
	img := inst["image"].(map[string]any)["bytesBase64Encoded"].(string)
	if got, _ := base64.StdEncoding.DecodeString(img); string(got) != "SRC" {
		t.Errorf("embedded bytes = %q", got)
	}
}

// A parameter naming a file is still a path an agent chose.
func TestFileInputCannotEscapeTheWorktree(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true }))
	defer srv.Close()

	r, _ := toolWith(t, srv.URL, HTTPTool{
		Name: "edit", Path: "/x", Body: `{}`,
		Inputs: map[string]ToolInput{"image": {As: "multipart"}},
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	out, isErr := r.Dispatch("edit", map[string]any{"image": "../../etc/hosts", "path": "o.png"})
	if !isErr {
		t.Fatalf("escape accepted: %s", out)
	}
	if called {
		t.Error("the provider was called with a rejected path")
	}
}

func TestFileInputIsSizeCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	r, work := toolWith(t, srv.URL, HTTPTool{
		Name: "edit", Path: "/x", Body: `{}`,
		Inputs: map[string]ToolInput{"image": {As: "multipart", MaxBytes: 16}},
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	os.WriteFile(filepath.Join(work, "big.png"), make([]byte, 64), 0o644)
	if out, isErr := r.Dispatch("edit", map[string]any{"image": "big.png", "path": "o.png"}); !isErr || !strings.Contains(out, "limit") {
		t.Errorf("oversized upload = %q (isErr=%v)", out, isErr)
	}
}

// An optional input the model skipped leaves the request as it would have been:
// "edit this" and "make a new one" can be the same tool.
func TestAnOmittedFileInputSendsPlainJSON(t *testing.T) {
	var ct string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ct = req.Header.Get("Content-Type")
		json.NewDecoder(req.Body).Decode(&body)
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	r, _ := toolWith(t, srv.URL, HTTPTool{
		Name: "gen", Path: "/x", Body: `{"prompt":"{{prompt}}"}`,
		Inputs: map[string]ToolInput{"image": {As: "multipart"}},
		Output: &ToolOutput{Kind: "binary", Extensions: []string{".png"}},
	})
	if out, isErr := r.Dispatch("gen", map[string]any{"prompt": "p", "path": "o.png"}); isErr {
		t.Fatalf("dispatch failed: %s", out)
	}
	if ct != "application/json" {
		t.Errorf("content type = %q, want plain JSON when no file was sent", ct)
	}
	if body["prompt"] != "p" {
		t.Errorf("body = %v", body)
	}
}

// A text tool can upload too — that is how "post this file" is expressed.
func TestATextToolCanUploadAFile(t *testing.T) {
	var sawFile bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, params, _ := mime.ParseMediaType(req.Header.Get("Content-Type"))
		mr := multipart.NewReader(req.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FileName() != "" {
				sawFile = true
			}
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r, work := toolWith(t, srv.URL, HTTPTool{
		Name: "upload", Path: "/slack/files.upload", Body: `{"channel":"{{channel}}"}`,
		Inputs: map[string]ToolInput{"file": {As: "multipart", Field: "file"}},
	})
	os.WriteFile(filepath.Join(work, "report.md"), []byte("hi"), 0o644)

	out, isErr := r.Dispatch("upload", map[string]any{"channel": "#general", "file": "report.md"})
	if isErr || !strings.Contains(out, "ok") {
		t.Fatalf("upload = %q (isErr=%v)", out, isErr)
	}
	if !sawFile {
		t.Error("no file part was sent")
	}
}
