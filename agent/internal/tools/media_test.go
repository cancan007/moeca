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
	"time"

	"orchestra/agent/internal/llm"
)

func mediaRegistry(t *testing.T, gateway string, cfg MediaConfig) (*Registry, string) {
	t.Helper()
	work := t.TempDir()
	r := New(work)
	r.SetMedia(gateway, llm.GatewayCtx{}, cfg)
	return r, work
}

// Only configured kinds are advertised. An agent template that was not granted
// video must not see a video tool at all.
func TestMediaDefinitionsFollowConfig(t *testing.T) {
	r, _ := mediaRegistry(t, "http://gw", MediaConfig{Image: &MediaSpec{Prefix: "/openai", Model: "m"}})
	names := map[string]bool{}
	for _, d := range r.Definitions() {
		names[d.Name] = true
	}
	if !names["generate_image"] {
		t.Errorf("generate_image should be advertised")
	}
	for _, absent := range []string{"generate_speech", "generate_video"} {
		if names[absent] {
			t.Errorf("%s should not be advertised when unconfigured", absent)
		}
	}

	none := New(t.TempDir())
	for _, d := range none.Definitions() {
		if strings.HasPrefix(d.Name, "generate_") {
			t.Errorf("no media config should mean no media tools, got %s", d.Name)
		}
	}
}

func TestGenerateImageWritesDecodedBytes(t *testing.T) {
	want := []byte("\x89PNG\r\n\x1a\npretend-pixels")
	var gotPath, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		if got, _ := body["size"].(string); got != "512x512" {
			t.Errorf("size = %q, want the caller's override", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(want)}},
		})
	}))
	defer srv.Close()

	r, work := mediaRegistry(t, srv.URL, MediaConfig{Image: &MediaSpec{
		Prefix: "/openai", Model: "gpt-image-1", Size: "1024x1024",
	}})
	out, isErr := r.Dispatch("generate_image", map[string]any{
		"prompt": "a chart", "path": "artifacts/chart.png", "size": "512x512",
	})
	if isErr {
		t.Fatalf("generate_image: %s", out)
	}
	if gotPath != "/openai/v1/images/generations" {
		t.Errorf("request path = %q", gotPath)
	}
	if gotModel != "gpt-image-1" {
		t.Errorf("model = %q", gotModel)
	}
	got, err := os.ReadFile(filepath.Join(work, "artifacts", "chart.png"))
	if err != nil {
		t.Fatalf("artifact not written: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("written bytes do not match the decoded payload")
	}
}

// A provider that hands back a URL is refused rather than followed: fetching it
// would be egress to a host the gateway never allowlisted.
func TestGenerateImageRefusesAURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"url": "https://cdn.example/x.png"}}})
	}))
	defer srv.Close()

	r, _ := mediaRegistry(t, srv.URL, MediaConfig{Image: &MediaSpec{Prefix: "/openai", Model: "m"}})
	out, isErr := r.Dispatch("generate_image", map[string]any{"prompt": "x", "path": "a.png"})
	if !isErr || !strings.Contains(out, "URL") {
		t.Fatalf("expected a refusal mentioning the URL, got isErr=%v %q", isErr, out)
	}
}

func TestGenerateSpeechWritesRawAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body)
		if got, _ := body["response_format"].(string); got != "wav" {
			t.Errorf("response_format = %q, want wav (taken from the path extension)", got)
		}
		if got, _ := body["voice"].(string); got != "alloy" {
			t.Errorf("voice = %q, want the spec default", got)
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Write([]byte("RIFFpretend-audio"))
	}))
	defer srv.Close()

	r, work := mediaRegistry(t, srv.URL, MediaConfig{Speech: &MediaSpec{
		Prefix: "/openai", Model: "tts-1", Voice: "alloy",
	}})
	out, isErr := r.Dispatch("generate_speech", map[string]any{"text": "こんにちは", "path": "out/voice.wav"})
	if isErr {
		t.Fatalf("generate_speech: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(work, "out", "voice.wav"))
	if err != nil || !strings.HasPrefix(string(got), "RIFF") {
		t.Fatalf("audio not written: %v %q", err, got)
	}
}

// A JSON body on the speech route is an error envelope that arrived with a 200.
// Writing it to disk would produce a .wav that is not audio.
func TestGenerateSpeechRejectsJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":{"message":"quota"}}`))
	}))
	defer srv.Close()

	r, work := mediaRegistry(t, srv.URL, MediaConfig{Speech: &MediaSpec{Prefix: "/openai", Model: "m"}})
	out, isErr := r.Dispatch("generate_speech", map[string]any{"text": "x", "path": "a.mp3"})
	if !isErr {
		t.Fatalf("expected an error, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(work, "a.mp3")); !os.IsNotExist(err) {
		t.Errorf("nothing should have been written")
	}
}

// Video is asynchronous: create, poll, then download.
func TestGenerateVideoPollsThenDownloads(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/openai/v1/videos":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"vid_1","status":"queued"}`))
		case req.URL.Path == "/openai/v1/videos/vid_1":
			polls++
			w.Header().Set("Content-Type", "application/json")
			if polls < 2 {
				w.Write([]byte(`{"id":"vid_1","status":"in_progress"}`))
				return
			}
			w.Write([]byte(`{"id":"vid_1","status":"completed"}`))
		case req.URL.Path == "/openai/v1/videos/vid_1/content":
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("\x00\x00\x00\x18ftypmp42"))
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
		}
	}))
	defer srv.Close()

	r, work := mediaRegistry(t, srv.URL, MediaConfig{Video: &MediaSpec{Prefix: "/openai", Model: "sora-2"}})
	// Keep the test quick; the production cadence is deliberately slow.
	old := pollEvery
	pollEvery = time.Millisecond
	defer func() { pollEvery = old }()

	out, isErr := r.Dispatch("generate_video", map[string]any{"prompt": "a demo", "path": "artifacts/demo.mp4"})
	if isErr {
		t.Fatalf("generate_video: %s", out)
	}
	if polls < 2 {
		t.Errorf("expected the job to be polled until completed, got %d polls", polls)
	}
	if _, err := os.Stat(filepath.Join(work, "artifacts", "demo.mp4")); err != nil {
		t.Fatalf("video not written: %v", err)
	}
}

func TestGenerateVideoReportsAFailedJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPost {
			w.Write([]byte(`{"id":"vid_2","status":"failed","error":"content policy"}`))
			return
		}
		t.Errorf("a failed job must not be polled or downloaded")
	}))
	defer srv.Close()

	r, _ := mediaRegistry(t, srv.URL, MediaConfig{Video: &MediaSpec{Prefix: "/openai", Model: "m"}})
	out, isErr := r.Dispatch("generate_video", map[string]any{"prompt": "x", "path": "a.mp4"})
	if !isErr || !strings.Contains(out, "content policy") {
		t.Fatalf("expected the provider's reason, got isErr=%v %q", isErr, out)
	}
}

// The extension decides how the artifact is classified and whether the host
// will serve it inline, so it is checked before anything is generated.
func TestMediaRefusesWrongExtension(t *testing.T) {
	r, _ := mediaRegistry(t, "http://unused", MediaConfig{
		Image:  &MediaSpec{Prefix: "/openai", Model: "m"},
		Speech: &MediaSpec{Prefix: "/openai", Model: "m"},
		Video:  &MediaSpec{Prefix: "/openai", Model: "m"},
	})
	cases := []struct{ tool, path string }{
		{"generate_image", "run.sh"},
		{"generate_image", "notes.md"},
		{"generate_speech", "voice.png"},
		{"generate_video", "clip.gif"},
	}
	for _, c := range cases {
		out, isErr := r.Dispatch(c.tool, map[string]any{
			"prompt": "x", "text": "x", "path": c.path,
		})
		if !isErr {
			t.Errorf("%s(%s) should be refused, got %q", c.tool, c.path, out)
		}
	}
}

// Path safety is the file tools' rule, and generation does not get its own.
func TestMediaRefusesEscapingPath(t *testing.T) {
	r, _ := mediaRegistry(t, "http://unused", MediaConfig{Image: &MediaSpec{Prefix: "/openai", Model: "m"}})
	out, isErr := r.Dispatch("generate_image", map[string]any{"prompt": "x", "path": "../escape.png"})
	if !isErr {
		t.Fatalf("escaping path should be refused, got %q", out)
	}
}
