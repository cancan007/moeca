package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No dialect takes an mp4, so a video is checked as stills — and the extraction
// runs in the media image rather than in the agent's, which is distroless on
// purpose: confining the media parsers is the whole reason that image exists.

func frameRequest(t *testing.T, work string, req framesRequest) string {
	t.Helper()
	dir := filepath.Join(work, ".orchestra", "frames", req.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(req)
	if err := os.WriteFile(filepath.Join(dir, "request.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func frameResult(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatalf("no result written: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFrameExtractionRunsInTheMediaImage(t *testing.T) {
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "clip.mp4"), []byte("video"), 0o644)
	fake := &fakeDocker{}
	s := newWith(imagesConfig(t), fake)

	dir := frameRequest(t, work, framesRequest{ID: "abc", Path: "clip.mp4", Frames: 3, OutDir: ".orchestra/frames/abc"})
	// Pretend ffmpeg produced them: the fake docker runs nothing.
	os.WriteFile(filepath.Join(dir, "frame-01.png"), []byte("\x89PNG"), 0o644)
	s.processFrameRequests(filepath.Join(work, ".orchestra", "frames"), work, true)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 1 {
		t.Fatalf("created %d containers, want 1", len(fake.created))
	}
	spec := fake.created[0]
	if !strings.Contains(spec.Image, "media") {
		t.Errorf("extraction ran in %q, want the media image", spec.Image)
	}
	cmd := strings.Join(spec.Cmd, " ")
	if !strings.Contains(cmd, "ffmpeg") || !strings.Contains(cmd, "clip.mp4") {
		t.Errorf("command = %q", cmd)
	}
	// Spread across the file, not the first three frames.
	if !strings.Contains(cmd, "thumbnail") {
		t.Errorf("command does not sample across the video: %q", cmd)
	}
	if got := frameResult(t, dir)["frames"]; got == nil {
		t.Error("the produced frames were not reported back")
	}
}

// The request is a file an agent wrote, so it is input. A path pointing out of
// the worktree must be refused here, at the boundary.
func TestFrameRequestCannotEscapeTheWorktree(t *testing.T) {
	work := t.TempDir()
	fake := &fakeDocker{}
	s := newWith(imagesConfig(t), fake)

	for _, bad := range []string{"../../etc/passwd", "/etc/hosts"} {
		dir := frameRequest(t, work, framesRequest{ID: sanitizeID(bad), Path: bad, Frames: 1, OutDir: ".orchestra/frames/x"})
		s.processFrameRequests(filepath.Join(work, ".orchestra", "frames"), work, true)
		if msg, _ := frameResult(t, dir)["error"].(string); msg == "" {
			t.Errorf("path %q was accepted", bad)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 0 {
		t.Error("a container ran for a rejected path")
	}
}

// A request already answered is not answered twice: the agent is polling for
// exactly one result.
func TestFrameRequestIsFulfilledOnce(t *testing.T) {
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "clip.mp4"), []byte("v"), 0o644)
	fake := &fakeDocker{}
	s := newWith(imagesConfig(t), fake)

	frameRequest(t, work, framesRequest{ID: "abc", Path: "clip.mp4", Frames: 1, OutDir: ".orchestra/frames/abc"})
	base := filepath.Join(work, ".orchestra", "frames")
	s.processFrameRequests(base, work, true)
	s.processFrameRequests(base, work, true)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 1 {
		t.Errorf("ran %d extractions, want 1", len(fake.created))
	}
}

// More frames than the cap would be a context bill the agent did not mean to
// run up.
func TestFrameCountIsCapped(t *testing.T) {
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "clip.mp4"), []byte("v"), 0o644)
	fake := &fakeDocker{}
	s := newWith(imagesConfig(t), fake)

	frameRequest(t, work, framesRequest{ID: "abc", Path: "clip.mp4", Frames: 99, OutDir: ".orchestra/frames/abc"})
	s.processFrameRequests(filepath.Join(work, ".orchestra", "frames"), work, true)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	cmd := strings.Join(fake.created[0].Cmd, " ")
	if !strings.Contains(cmd, "-frames:v 8") {
		t.Errorf("frame count was not capped: %q", cmd)
	}
}
