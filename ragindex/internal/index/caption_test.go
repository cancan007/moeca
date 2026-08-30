package index

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A 1x1 PNG, so a "picture" exists without a fixture file.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
}

// visionServer answers the chat-completions shape and counts the calls, which
// is the number that decides whether this feature is affordable.
func visionServer(t *testing.T, caption string, calls *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("vision call went to %s", r.URL.Path)
		}
		*calls++
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": caption}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func imageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Off by default: registering a folder of pictures must not start spending.
func TestImagesStayMetadataOnlyWithoutACaptionModel(t *testing.T) {
	calls := 0
	vision := visionServer(t, "never asked", &calls)
	idx := New(Config{
		Sources:   []SourceSpec{{Kind: KindLocal, Root: imageDir(t)}},
		Gateway:   vision.URL,
		EmbedMode: EmbedModeOffline,
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("made %d vision calls with captioning off", calls)
	}
	for _, s := range idx.Status().Sources {
		if s.Content != ContentMetadata {
			t.Errorf("content = %q, want %q", s.Content, ContentMetadata)
		}
	}
}

// On: the picture becomes findable by what is in it.
func TestCaptionedImageIsSearchableByItsContents(t *testing.T) {
	calls := 0
	vision := visionServer(t, "ネイビーのタートルネックを着た黒柴のイラスト", &calls)
	idx := New(Config{
		Sources:      []SourceSpec{{Kind: KindLocal, Root: imageDir(t)}},
		Gateway:      vision.URL,
		EmbedPrefix:  "/openai",
		EmbedMode:    EmbedModeOffline,
		CaptionModel: "gpt-4o-mini",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("vision calls = %d, want 1", calls)
	}

	var src Source
	for _, s := range idx.Status().Sources {
		if s.Path == "logo.png" {
			src = s
		}
	}
	// Not "text": the words are a model's account of the picture, not anything
	// written in the file, and the UI must be able to tell them apart.
	if src.Content != ContentCaption {
		t.Errorf("content = %q, want %q", src.Content, ContentCaption)
	}
	if src.Chunks == 0 {
		t.Error("a captioned image should have indexed text")
	}

	res, err := idx.Search(context.Background(), "黒柴", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || !strings.Contains(res[0].Text, "黒柴") {
		t.Errorf("the caption did not reach the index: %+v", res)
	}
	// The descriptor survives alongside it, so searching by filename still works.
	if !strings.Contains(res[0].Text, "logo.png") {
		t.Errorf("the descriptor was replaced rather than added to: %q", res[0].Text)
	}
}

// The whole reason this is affordable: a rebuild re-reads everything locally and
// pays the model only for pictures it has not seen.
func TestARebuildDoesNotReCaptionAnUnchangedImage(t *testing.T) {
	calls := 0
	vision := visionServer(t, "説明", &calls)
	dir := imageDir(t)
	cfg := Config{
		Sources:      []SourceSpec{{Kind: KindLocal, Root: dir}},
		Gateway:      vision.URL,
		EmbedMode:    EmbedModeOffline,
		CaptionModel: "gpt-4o-mini",
		CacheDir:     t.TempDir(),
	}
	idx := New(cfg)
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("vision calls = %d after two builds, want 1", calls)
	}

	// And across a restart, which is what the cache file is for.
	restarted := New(cfg)
	restarted.LoadCaptions()
	if err := restarted.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("vision calls = %d after a restart, want 1", calls)
	}
}

// Keyed by content, so replacing the picture re-describes it. A caption that
// outlived the image it described would be worse than none.
func TestReplacingTheImageTakesANewCaption(t *testing.T) {
	calls := 0
	vision := visionServer(t, "説明", &calls)
	dir := imageDir(t)
	idx := New(Config{
		Sources:      []SourceSpec{{Kind: KindLocal, Root: dir}},
		Gateway:      vision.URL,
		EmbedMode:    EmbedModeOffline,
		CaptionModel: "gpt-4o-mini",
		CacheDir:     t.TempDir(),
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "logo.png"), append(append([]byte{}, tinyPNG...), 0x00), 0o644)
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("vision calls = %d, want 2 — different bytes are a different picture", calls)
	}
}

// A model that will not answer must not fail the build. The image goes back to
// being indexed by name, which is exactly where it was before captioning.
func TestACaptionFailureLeavesTheImageIndexedByName(t *testing.T) {
	vision := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"no key"}`, http.StatusUnauthorized)
	}))
	defer vision.Close()

	idx := New(Config{
		Sources:      []SourceSpec{{Kind: KindLocal, Root: imageDir(t)}},
		Gateway:      vision.URL,
		EmbedMode:    EmbedModeOffline,
		CaptionModel: "gpt-4o-mini",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("one undescribable picture must not fail a build: %v", err)
	}
	src := idx.Status().Sources[0]
	if src.Content != ContentMetadata {
		t.Errorf("content = %q, want %q", src.Content, ContentMetadata)
	}
	if src.Note == "" || !strings.Contains(src.Note, "失敗") {
		t.Errorf("note = %q, want it to say why this picture is thinner", src.Note)
	}
	if src.Chunks == 0 {
		t.Error("the descriptor should still be indexed")
	}
}

// The model and the prompt are part of the key: a description written by a
// different model answering a different question is a different caption.
func TestCaptionKeyCoversModelAndPrompt(t *testing.T) {
	a := captionKey(tinyPNG, "gpt-4o-mini", "1")
	if a == captionKey(tinyPNG, "gpt-4o", "1") {
		t.Error("two models share a cache entry")
	}
	if a == captionKey(tinyPNG, "gpt-4o-mini", "2") {
		t.Error("two prompt versions share a cache entry")
	}
	if a != captionKey(tinyPNG, "gpt-4o-mini", "1") {
		t.Error("the key is not stable for identical inputs")
	}
}

// Video is deliberately left alone: describing one needs frame sampling, and
// that means ffmpeg beside all of the knowledge.
func TestVideoIsNotCaptioned(t *testing.T) {
	calls := 0
	vision := visionServer(t, "説明", &calls)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.mp4"), []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := New(Config{
		Sources:      []SourceSpec{{Kind: KindLocal, Root: dir}},
		Gateway:      vision.URL,
		EmbedMode:    EmbedModeOffline,
		CaptionModel: "gpt-4o-mini",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("made %d vision calls for a video", calls)
	}
	if src := idx.Status().Sources[0]; src.Content != ContentMetadata {
		t.Errorf("content = %q, want %q", src.Content, ContentMetadata)
	}
}

func TestCaptionPrefixFallsBackToTheEmbedRoute(t *testing.T) {
	i := &Index{cfg: Config{EmbedPrefix: "/openai"}}
	if got := i.captionPrefixOr(); got != "/openai" {
		t.Errorf("captionPrefixOr() = %q", got)
	}
	i.cfg.CaptionPrefix = "/gemini"
	if got := i.captionPrefixOr(); got != "/gemini" {
		t.Errorf("captionPrefixOr() = %q, want the configured route", got)
	}
}
