// Looking at a video.
//
// view_image works because every dialect takes an image in a user turn. None of
// them takes an mp4: Anthropic and the OpenAI chat models cannot, so a run whose
// output is a video had no way to be checked by the agent that produced it —
// the integrator could read the filename and nothing else.
//
// So a video is looked at the way a person looks at one they cannot play:
// stills, spread across its length. That works on every dialect, and several
// moments are arguably a better check than one — a video can be right at the
// first frame and wrong a second later.
//
// The extraction itself does NOT happen here. This container is distroless: it
// has no ffmpeg, and putting one in would undo the reason the media image exists
// (see agent/Dockerfile.media — the media parsers are confined to the stage that
// needs them precisely so every other agent does not inherit their CVE history).
// Instead the agent asks, through the same file-based channel delegation uses,
// and the controller runs the media image — networkless, ffmpeg-bearing — over
// the same worktree. The agent opens no path to the host; it writes a file in
// its own /work and waits for one to appear next to it.
package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestra/agent/internal/llm"
)

// framesDir is where a frame request and its result live, mirroring
// .orchestra/delegate. The controller watches it.
const framesDir = ".orchestra/frames"

// Frame extraction is a container start plus an ffmpeg pass over a file that is
// already local, so it is fast — but it queues behind the controller's poll.
const (
	framesTimeout  = 5 * time.Minute
	framesPoll     = 500 * time.Millisecond
	defaultFrames  = 3
	maxFrames      = 8
	viewableVideos = ".mp4, .webm, .mov, .mkv"
)

// videoExts are the containers worth trying. The extraction runs ffmpeg, which
// reads far more than this; the list exists so that asking to view a .txt is
// refused here rather than by a container that starts and fails.
var videoExts = map[string]bool{".mp4": true, ".webm": true, ".mov": true, ".mkv": true}

func viewVideoDef() llm.Tool {
	return llm.Tool{
		Name: "view_video",
		Description: "Look at a video in /work by sampling still frames from it, evenly spread across its length. " +
			"Use it to check that a generated video is what was asked for — no model can watch the file itself. " +
			"Supports " + viewableVideos + ". Each frame costs as much as an image, so ask for few.",
		InputSchema: schema(map[string]string{
			"path":   "Path of the video, relative to /work.",
			"frames": fmt.Sprintf("How many stills to sample, 1-%d. Default %d.", maxFrames, defaultFrames),
		}, "path"),
	}
}

// framesRequest is what the controller reads. Paths are worktree-relative on
// both sides: the agent knows /work, the controller knows where that is.
type framesRequest struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Frames int    `json:"frames"`
	OutDir string `json:"outDir"`
}

type framesResult struct {
	Frames []string `json:"frames"`
	Error  string   `json:"error"`
}

// viewVideo asks the controller for stills and attaches them for the model.
func (r *Registry) viewVideo(rel, framesArg string) (string, bool) {
	rel = strings.TrimSpace(rel)
	if _, err := r.resolve(rel); err != nil {
		return err.Error(), true
	}
	if !videoExts[strings.ToLower(filepath.Ext(rel))] {
		return fmt.Sprintf("%s is not a video this can sample (%s). Videos: %s. For a still image use view_image.",
			rel, filepath.Ext(rel), viewableVideos), true
	}
	if _, err := os.Stat(filepath.Join(r.root, filepath.Clean(rel))); err != nil {
		return fmt.Sprintf("view_video failed: %v", err), true
	}

	n := defaultFrames
	if v := strings.TrimSpace(framesArg); v != "" {
		if parsed := atoiOr(v, defaultFrames); parsed > 0 {
			n = parsed
		}
	}
	if n > maxFrames {
		n = maxFrames
	}

	id, err := randID()
	if err != nil {
		return "view_video: " + err.Error(), true
	}
	dir := filepath.Join(r.root, framesDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "view_video: " + err.Error(), true
	}
	outRel := filepath.ToSlash(filepath.Join(framesDir, id))
	body, _ := json.MarshalIndent(framesRequest{ID: id, Path: rel, Frames: n, OutDir: outRel}, "", "  ")
	// Written under a temp name and renamed, so the watcher never reads a
	// half-written request.
	tmp := filepath.Join(dir, "request.json.tmp")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "view_video: " + err.Error(), true
	}
	if err := os.Rename(tmp, filepath.Join(dir, "request.json")); err != nil {
		return "view_video: " + err.Error(), true
	}

	res, err := r.awaitFrames(filepath.Join(dir, "result.json"))
	if err != nil {
		return "view_video: " + err.Error(), true
	}
	if res.Error != "" {
		return "could not sample frames from " + rel + ": " + res.Error, true
	}
	if len(res.Frames) == 0 {
		return "no frames came back from " + rel + "; it may be empty or unreadable", true
	}

	attached := 0
	for _, f := range res.Frames {
		abs, err := r.resolve(f)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil || len(b) == 0 || len(b) > maxViewBytes {
			continue
		}
		r.mu.Lock()
		r.attachments = append(r.attachments, attachment{Path: f, MediaType: "image/png", Data: b64(b)})
		r.mu.Unlock()
		attached++
	}
	if attached == 0 {
		return "frames were produced but none could be read back", true
	}
	return fmt.Sprintf("sampled %d frames from %s, evenly spread across it — they follow this result, in order", attached, rel), false
}

func (r *Registry) awaitFrames(path string) (framesResult, error) {
	deadline := time.Now().Add(framesTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err != nil {
			time.Sleep(framesPoll)
			continue
		}
		var res framesResult
		if json.Unmarshal(data, &res) != nil {
			return res, fmt.Errorf("unreadable result")
		}
		return res, nil
	}
	return framesResult{}, fmt.Errorf("timed out waiting for frames (is the controller running a media image?)")
}
