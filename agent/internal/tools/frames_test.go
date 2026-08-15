package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The agent cannot extract a frame itself — its image is distroless on purpose
// — so it asks through its own worktree and waits. These pin that contract.

// answerFrames plays the controller: it waits for a request and writes a result.
func answerFrames(t *testing.T, root string, frames []string, errMsg string) {
	t.Helper()
	go func() {
		base := filepath.Join(root, ".orchestra", "frames")
		for i := 0; i < 200; i++ {
			entries, err := os.ReadDir(base)
			if err == nil {
				for _, e := range entries {
					dir := filepath.Join(base, e.Name())
					if _, err := os.Stat(filepath.Join(dir, "request.json")); err != nil {
						continue
					}
					if _, err := os.Stat(filepath.Join(dir, "result.json")); err == nil {
						continue
					}
					for _, f := range frames {
						os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o755)
						os.WriteFile(filepath.Join(root, f), []byte("\x89PNGframe"), 0o644)
					}
					body, _ := json.Marshal(map[string]any{"frames": frames, "error": errMsg})
					os.WriteFile(filepath.Join(dir, "result.json"), body, 0o644)
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func TestViewVideoAttachesTheSampledFrames(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "cat.mp4"), []byte("video"), 0o644)
	r := New(root)
	answerFrames(t, root, []string{"frames/a.png", "frames/b.png"}, "")

	out, isErr := r.Dispatch("view_video", map[string]any{"path": "cat.mp4", "frames": "2"})
	if isErr {
		t.Fatalf("view_video failed: %s", out)
	}
	blocks := r.TakeAttachments()
	if len(blocks) != 2 {
		t.Fatalf("attached %d frames, want 2", len(blocks))
	}
	for _, b := range blocks {
		if b.MediaType != "image/png" || b.Data == "" {
			t.Errorf("frame block = %+v", b)
		}
	}
	if !strings.Contains(out, "cat.mp4") {
		t.Errorf("result = %q, want it to name the video", out)
	}
}

// The controller's own reason reaches the model — an unreadable video is a
// thing the agent has to be able to act on.
func TestViewVideoReportsTheExtractionFailure(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "broken.mp4"), []byte("x"), 0o644)
	r := New(root)
	answerFrames(t, root, nil, "ffmpeg exited 1: moov atom not found")

	out, isErr := r.Dispatch("view_video", map[string]any{"path": "broken.mp4"})
	if !isErr || !strings.Contains(out, "moov atom") {
		t.Errorf("result = %q (isErr=%v), want the extraction error", out, isErr)
	}
}

// An image is not a video: pointing the wrong tool at a file should say which
// tool to use rather than start a container.
func TestViewVideoRefusesAnImage(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.png"), []byte("\x89PNG"), 0o644)
	r := New(root)
	if out, isErr := r.Dispatch("view_video", map[string]any{"path": "a.png"}); !isErr || !strings.Contains(out, "view_image") {
		t.Errorf("result = %q (isErr=%v)", out, isErr)
	}
}

// And the reverse, so neither tool leaves the model guessing.
func TestViewImageSendsVideosToViewVideo(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.mp4"), []byte("v"), 0o644)
	r := New(root)
	out, isErr := r.Dispatch("view_image", map[string]any{"path": "a.mp4"})
	if !isErr || !strings.Contains(out, "view_video") {
		t.Errorf("result = %q (isErr=%v)", out, isErr)
	}
}

func TestViewVideoRejectsAMissingFile(t *testing.T) {
	r := New(t.TempDir())
	if out, isErr := r.Dispatch("view_video", map[string]any{"path": "nope.mp4"}); !isErr {
		t.Errorf("a missing video was accepted: %s", out)
	}
}

func TestViewVideoIsAdvertised(t *testing.T) {
	var found bool
	for _, d := range New(t.TempDir()).Definitions() {
		if d.Name == "view_video" {
			found = true
		}
	}
	if !found {
		t.Error("view_video is not in the tool list")
	}
}
