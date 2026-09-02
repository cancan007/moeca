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

// What a provider actually receives for an uploaded file.
//
// Every image edit route rejects a part typed application/octet-stream, and the
// filename is the other half of how it decides what it was sent. Both were wrong
// — CreateFormFile hardcodes the type and was being handed the PARAMETER name —
// so every edit call failed with "unsupported mimetype" on a file that was
// perfectly valid on disk. Nothing caught it because the tests asserted the file
// arrived, not how it was labelled.
func TestAnUploadedFileIsTypedAndNamed(t *testing.T) {
	// A real JPEG header, so the sniffer has something to recognise.
	jpeg := append([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}, make([]byte, 600)...)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "refs", "kon_sitting.jpg"), jpeg, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotType, gotDisposition string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("not a multipart request: %v", err)
			return
		}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			if p.FormName() == "image" {
				gotType = p.Header.Get("Content-Type")
				gotDisposition = p.Header.Get("Content-Disposition")
			}
		}
		w.Write([]byte(`{"data":[{"b64_json":"aGk="}]}`))
	}))
	defer srv.Close()

	r := New(dir)
	r.SetHTTP(srv.URL, llm.GatewayCtx{}, []HTTPTool{{
		Name:   "edit_image",
		Method: "POST",
		Path:   "/v1/images/edits",
		Body:   `{"model":"gpt-image-1","prompt":"{{prompt}}"}`,
		Inputs: map[string]ToolInput{"image": {As: "multipart", Field: "image"}},
		Output: &ToolOutput{Kind: "base64", JSONPath: "data.0.b64_json", Extensions: []string{".png"}},
	}})
	out, isErr := r.Dispatch("edit_image", map[string]any{
		"image":  "refs/kon_sitting.jpg",
		"prompt": "remove the watermark",
		"path":   "out.png",
	})
	if isErr {
		t.Fatalf("call failed: %s", out)
	}

	if gotType != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg — octet-stream is what every image route rejects", gotType)
	}
	// The file's own name, not the parameter's: a provider that falls back to
	// the extension has nothing to read in "image".
	if !strings.Contains(gotDisposition, `filename="kon_sitting.jpg"`) {
		t.Errorf("Content-Disposition = %q, want the file's own name", gotDisposition)
	}
}

// Content decides, not the extension: a file misnamed .jpg that is really a PNG
// is sent as what it is.
func TestAnUploadIsTypedByItsContentNotItsName(t *testing.T) {
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, make([]byte, 600)...)
	if got := contentTypeOf(inputFile{name: "lying.jpg", data: png}); got != "image/png" {
		t.Errorf("contentTypeOf = %q, want image/png", got)
	}
}

// A format the sniffer does not know falls back to the name, and to a bare type
// with no charset — a form part does not want one and providers match on the
// bare value.
func TestAnUnsniffableUploadFallsBackToItsExtension(t *testing.T) {
	got := contentTypeOf(inputFile{name: "caption.vtt", data: []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nhi\n")})
	if strings.Contains(got, ";") {
		t.Errorf("contentTypeOf = %q, want no charset parameter", got)
	}
	if got == "application/octet-stream" {
		t.Errorf("contentTypeOf = %q, want the extension's type", got)
	}
}

// A provider that wants a nested object wants it absent rather than present and
// hollow. Sora's input_reference is the case: with no reference supplied,
// `{"file_id":""}` is rejected, and the tool has to be usable without one.
func TestAnUnfilledNestedObjectIsRemoved(t *testing.T) {
	got := pruneBody(`{"model":"sora-2","prompt":"a dog","input_reference":{"file_id":"{{file_id}}"}}`)
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("pruned body is not JSON: %v", err)
	}
	if _, present := doc["input_reference"]; present {
		t.Errorf("input_reference survived unfilled: %s", got)
	}
	if doc["prompt"] != "a dog" {
		t.Errorf("pruning took a supplied value: %s", got)
	}
}

// And when it is supplied, the object stays whole.
func TestAFilledNestedObjectSurvives(t *testing.T) {
	got := pruneBody(`{"model":"sora-2","prompt":"a dog","input_reference":{"file_id":"file-abc123"}}`)
	var doc map[string]any
	json.Unmarshal([]byte(got), &doc)
	ref, ok := doc["input_reference"].(map[string]any)
	if !ok || ref["file_id"] != "file-abc123" {
		t.Errorf("a supplied reference was altered: %s", got)
	}
}

// An object that is emptied by pruning goes too: an object with nothing in it
// still says the caller supplied one, which is the claim being retracted.
func TestAnObjectEmptiedByPruningIsRemoved(t *testing.T) {
	got := pruneBody(`{"a":"kept","nested":{"x":"","y":"{{unset}}"}}`)
	var doc map[string]any
	json.Unmarshal([]byte(got), &doc)
	if _, present := doc["nested"]; present {
		t.Errorf("an emptied object survived: %s", got)
	}
	if doc["a"] != "kept" {
		t.Errorf("pruning took a supplied value: %s", got)
	}
}
