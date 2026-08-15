package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Sampling still frames from a video, so an agent can look at one.
//
// No dialect the agent speaks takes an mp4, so a video is checked as stills.
// Extracting them needs ffmpeg, and ffmpeg deliberately does not exist in the
// agent image: the media parsers are confined to the media image precisely so
// that every other agent does not inherit a decade of their CVE history
// (agent/Dockerfile.media says so at length). Putting one in the base image to
// serve this feature would spend that isolation on a convenience.
//
// So the agent asks and the controller performs, over the same file-based
// channel delegation uses — the agent writes into its own worktree and waits;
// it opens no path to the host. The work runs in the media image, which the
// image policy gives NO network at all, which is the right posture for a
// process about to parse a file an agent generated.
const (
	framesSubdir    = ".orchestra/frames"
	framesPollEvery = 500 * time.Millisecond
	framesImageName = "media"
	framesMaxCount  = 8
)

// framesRequest mirrors the agent's request shape (see agent/internal/tools).
type framesRequest struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Frames int    `json:"frames"`
	OutDir string `json:"outDir"`
}

// watchFrames fulfils frame requests for one stage until it exits.
//
// Unlike delegation this is always on: looking at what you produced is not a
// privilege a template grants, and a stage that never asks pays only for a
// ticker reading an empty directory.
func (s *Server) watchFrames(worktreeDir string, strict bool, done <-chan struct{}) {
	base := filepath.Join(worktreeDir, filepath.FromSlash(framesSubdir))
	ticker := time.NewTicker(framesPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			s.processFrameRequests(base, worktreeDir, strict)
		}
	}
}

func (s *Server) processFrameRequests(base, worktreeDir string, strict bool) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return // nothing has asked yet
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "result.json")); err == nil {
			continue // already answered
		}
		raw, err := os.ReadFile(filepath.Join(dir, "request.json"))
		if err != nil {
			continue // still being written (the rename has not landed)
		}
		var req framesRequest
		if json.Unmarshal(raw, &req) != nil {
			writeFramesResult(dir, nil, "invalid request")
			continue
		}
		s.extractFrames(dir, worktreeDir, strict, req)
	}
}

// extractFrames runs one ffmpeg pass in the media image and reports the stills
// it produced.
func (s *Server) extractFrames(dir, worktreeDir string, strict bool, req framesRequest) {
	// The agent chose both paths, so both are checked here rather than trusted:
	// this is the boundary, and a request is just a file an agent wrote.
	src, err := safeRel(req.Path)
	if err != nil {
		writeFramesResult(dir, nil, err.Error())
		return
	}
	out, err := safeRel(req.OutDir)
	if err != nil {
		writeFramesResult(dir, nil, err.Error())
		return
	}
	n := req.Frames
	if n <= 0 {
		n = 1
	}
	if n > framesMaxCount {
		n = framesMaxCount
	}

	policy, err := s.resolveImage(framesImageName, false)
	if err != nil {
		writeFramesResult(dir, nil, "the media image is not available on this install: "+err.Error())
		return
	}
	digest, err := s.docker.Resolve(policy.Ref)
	if err != nil {
		writeFramesResult(dir, nil, "media image could not be resolved: "+err.Error())
		return
	}
	pinned := policy
	pinned.Ref = digest

	// thumbnail picks n frames spread over the whole file rather than the first
	// n: a video is usually wrong somewhere in the middle, not at frame one.
	cmd := []string{"bash", "-lc", fmt.Sprintf(
		"set -e; ffmpeg -v error -i %s -vf thumbnail,fps=1 -frames:v %d -vsync vfr %s/frame-%%02d.png",
		shellQuote("/work/"+src), n, shellQuote("/work/"+out))}

	taskID := sanitizeID("frames-" + req.ID)
	spec := s.buildSpec(taskID, worktreeDir, pinned, cmd, map[string]string{}, strict, "")
	cid, err := s.docker.Create(spec)
	if err != nil {
		writeFramesResult(dir, nil, err.Error())
		return
	}
	code, werr := s.docker.Wait(cid)
	logs, _ := s.docker.Logs(cid)
	_ = s.docker.Remove(cid)
	if werr != nil {
		writeFramesResult(dir, nil, werr.Error())
		return
	}
	if code != 0 {
		// ffmpeg's own complaint is the useful part; a bare exit code is not.
		writeFramesResult(dir, nil, fmt.Sprintf("ffmpeg exited %d: %s", code, truncate(logs, 300)))
		return
	}
	writeFramesResult(dir, collectFrames(worktreeDir, out), "")
}

// collectFrames lists the stills that landed, in order.
func collectFrames(worktreeDir, outRel string) []string {
	entries, err := os.ReadDir(filepath.Join(worktreeDir, filepath.FromSlash(outRel)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		out = append(out, outRel+"/"+e.Name())
	}
	return out
}

func writeFramesResult(dir string, frames []string, errMsg string) {
	body, _ := json.MarshalIndent(map[string]any{"frames": frames, "error": errMsg}, "", "  ")
	tmp := filepath.Join(dir, "result.json.tmp")
	if os.WriteFile(tmp, body, 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, "result.json"))
	}
}

// safeRel refuses a path that is absolute or escapes the worktree. The request
// came from an agent, so it is input, not instruction.
func safeRel(rel string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(rel))
	if clean == "" || clean == "." {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path %q: must be inside /work", rel)
	}
	return filepath.ToSlash(clean), nil
}

// shellQuote makes a path safe inside the single command string the media image
// runs. The paths are checked above, but the command is a shell line and a
// quote is what keeps it one argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
