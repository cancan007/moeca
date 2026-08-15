package tools

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
)

// An agent could always be told a file exists. None of that let it see the
// picture — so a run could generate an image, hand it to an integrator whose
// whole job was to check it, and the integrator would sign off having looked at
// a filename.

func pngFixture(t *testing.T, root, name string, size int) []byte {
	t.Helper()
	b := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, size)...)
	if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestViewImageAttachesTheBytesForTheModel(t *testing.T) {
	root := t.TempDir()
	want := pngFixture(t, root, "shiba.png", 64)
	r := New(root)

	out, isErr := r.Dispatch("view_image", map[string]any{"path": "shiba.png"})
	if isErr {
		t.Fatalf("view_image failed: %s", out)
	}
	blocks := r.TakeAttachments()
	if len(blocks) != 1 {
		t.Fatalf("attachments = %d, want 1", len(blocks))
	}
	if blocks[0].Type != llm.BlockImage || blocks[0].MediaType != "image/png" {
		t.Errorf("block = %+v", blocks[0])
	}
	if got, _ := base64.StdEncoding.DecodeString(blocks[0].Data); string(got) != string(want) {
		t.Error("the attached bytes are not the file's")
	}
	// The result text says what happened; the picture is not in it.
	if !strings.Contains(out, "shiba.png") {
		t.Errorf("result = %q, want it to name the file", out)
	}
}

// Taking the attachments clears them: the same image must not be sent twice,
// which would double its cost for every remaining turn.
func TestAttachmentsAreTakenOnce(t *testing.T) {
	root := t.TempDir()
	pngFixture(t, root, "a.png", 8)
	r := New(root)
	r.Dispatch("view_image", map[string]any{"path": "a.png"})

	if len(r.TakeAttachments()) != 1 {
		t.Fatal("first take returned nothing")
	}
	if got := r.TakeAttachments(); got != nil {
		t.Errorf("second take returned %v, want nothing", got)
	}
}

// A video cannot be looked at by any of the three dialects, and saying so with
// the way forward beats a provider error.
func TestViewImageRefusesWhatIsNotAnImage(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("not an image"), 0o644)
	r := New(root)

	out, isErr := r.Dispatch("view_image", map[string]any{"path": "clip.mp4"})
	if !isErr {
		t.Fatalf("a video was accepted: %s", out)
	}
	if !strings.Contains(out, "frame") {
		t.Errorf("refusal = %q, want it to point at extracting a frame", out)
	}
	if len(r.TakeAttachments()) != 0 {
		t.Error("a refused file was attached anyway")
	}
}

// An image that would dominate the context for the rest of the run is refused,
// with its size, rather than sent.
func TestViewImageRefusesAnOversizedImage(t *testing.T) {
	root := t.TempDir()
	pngFixture(t, root, "huge.png", maxViewBytes+1)
	r := New(root)

	out, isErr := r.Dispatch("view_image", map[string]any{"path": "huge.png"})
	if !isErr || !strings.Contains(out, "too large") {
		t.Errorf("oversized view = %q (isErr=%v)", out, isErr)
	}
}

// The same path guard as every other file tool: viewing is reading.
func TestViewImageCannotEscapeTheWorktree(t *testing.T) {
	r := New(t.TempDir())
	if out, isErr := r.Dispatch("view_image", map[string]any{"path": "../../etc/hosts"}); !isErr {
		t.Errorf("escape was allowed: %s", out)
	}
}

// It is advertised, so a model knows it can look.
func TestViewImageIsAdvertised(t *testing.T) {
	var found bool
	for _, d := range New(t.TempDir()).Definitions() {
		if d.Name == "view_image" {
			found = true
		}
	}
	if !found {
		t.Error("view_image is not in the tool list")
	}
}
