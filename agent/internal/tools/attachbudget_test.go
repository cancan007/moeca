package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/agent/internal/llm"
)

// One turn's attachments have to fit in one request. Three separately-legal
// images went out as a single 10.9 MB body, past the gateway's 8 MiB cap, and
// the run died holding work it had already paid for.

// viewed queues n images of the given size and returns what the turn attaches.
func viewed(t *testing.T, sizes ...int) []llm.Block {
	t.Helper()
	work := t.TempDir()
	r := New(work)
	for i, size := range sizes {
		name := string(rune('a'+i)) + ".png"
		os.WriteFile(filepath.Join(work, name), make([]byte, size), 0o644)
		if out, isErr := r.Dispatch("view_image", map[string]any{"path": name}); isErr {
			t.Fatalf("view_image(%s) failed: %s", name, out)
		}
	}
	return r.TakeAttachments()
}

func imagesIn(blocks []llm.Block) int {
	n := 0
	for _, b := range blocks {
		if b.Type == llm.BlockImage {
			n++
		}
	}
	return n
}

func TestATurnAttachesWhatFitsAndDefersTheRest(t *testing.T) {
	// Three 2 MB images: base64 makes each ~2.7 MB, so two fit in 4 MiB.
	blocks := viewed(t, 2<<20, 2<<20, 2<<20)

	if got := imagesIn(blocks); got != 1 && got != 2 {
		t.Errorf("attached %d images; the turn's budget should have held some back", got)
	}
	// The model must be told which ones it is not seeing.
	var note string
	for _, b := range blocks {
		if b.Type == llm.BlockText {
			note = b.Text
		}
	}
	if note == "" {
		t.Fatal("images were held back with nothing said about it")
	}
	if !strings.Contains(note, "c.png") {
		t.Errorf("the note does not name the deferred image: %q", note)
	}
}

// A single image over the whole turn budget still goes: refusing it would make
// a legal view_image silently do nothing.
func TestOneOversizedImageIsStillAttached(t *testing.T) {
	blocks := viewed(t, 5<<20) // ~6.7 MB base64, over the 4 MiB turn budget
	if imagesIn(blocks) != 1 {
		t.Errorf("a lone image was dropped: %d attached", imagesIn(blocks))
	}
}

func TestSmallImagesAllFit(t *testing.T) {
	blocks := viewed(t, 100<<10, 100<<10, 100<<10)
	if imagesIn(blocks) != 3 {
		t.Errorf("attached %d of 3 small images", imagesIn(blocks))
	}
	for _, b := range blocks {
		if b.Type == llm.BlockText {
			t.Errorf("said something was deferred when nothing was: %q", b.Text)
		}
	}
}
