package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Daily artifacts.
//
// A scheduled run writes into a directory of its own (see launchScheduledRun) —
// no repository, no branch, no diff. These two endpoints are how the Daily
// gallery reads that directory back: one lists what a run produced, the other
// serves a single file so it can be previewed or downloaded.
//
// The occurrence id is the only handle a caller gets. It is resolved to a
// directory here, through the store; a caller never names a directory, so the
// reachable set is exactly "directories this app created for its own runs".

// Artifact is one file a scheduled run produced.
type Artifact struct {
	Path string `json:"path"` // relative to the run's output directory
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Kind buckets the file for the gallery: video | image | audio | text | file.
	Kind string `json:"kind"`
	// ModTime is RFC3339; artifacts are listed newest first.
	ModTime string `json:"modTime"`
}

// artifactKinds maps an extension to the gallery bucket it belongs in.
var artifactKinds = map[string]string{
	".mp4": "video", ".webm": "video", ".mov": "video", ".m4v": "video",
	".png": "image", ".jpg": "image", ".jpeg": "image", ".gif": "image", ".webp": "image", ".svg": "image",
	".mp3": "audio", ".wav": "audio", ".m4a": "audio", ".ogg": "audio", ".flac": "audio",
	".md": "text", ".txt": "text", ".json": "text", ".csv": "text", ".html": "text", ".yaml": "text", ".yml": "text",
}

func artifactKind(name string) string {
	if k, ok := artifactKinds[strings.ToLower(filepath.Ext(name))]; ok {
		return k
	}
	return "file"
}

// inlineTypes are the content types an artifact may be RENDERED with. Anything
// else is served as a download.
//
// This is a positive allowlist of media — formats a browser treats as data. The
// distinction is the point: an agent chooses what it writes, so a run can
// produce an .html or .svg, and rendering one inline would execute its script
// in the UI's own origin, the origin that talks to the loopback services and
// holds the admin token. Media cannot do that. Note that .svg is a gallery
// "image" but is deliberately NOT inline-able — it is XML with scripting, and
// the two lists disagree on purpose.
var inlineTypes = map[string]string{
	".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime", ".m4v": "video/mp4",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4", ".ogg": "audio/ogg", ".flac": "audio/flac",
	".pdf": "application/pdf",
}

// safeJoinDir resolves a caller-supplied relative path inside root, refusing
// anything that leaves it.
//
// The `..` check is the obvious half. The symlink resolution is the half that
// matters here: the directory's contents were written by an agent, so it can
// contain a link pointing anywhere on the host. A path check alone passes such
// a link — it is a perfectly ordinary relative path — and then ServeFile
// happily follows it. Comparing the fully-resolved paths is what closes that,
// and it has to resolve BOTH sides, since the root itself may sit under a
// symlinked prefix (/var → /private/var on macOS).
func safeJoinDir(root, rel string) (string, error) {
	if rel == "" {
		return "", errBadPath
	}
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errBadPath
	}
	abs := filepath.Join(root, clean)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errBadPath
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errBadPath // missing file, or a link to nowhere
	}
	if realPath != realRoot && !strings.HasPrefix(realPath, realRoot+string(filepath.Separator)) {
		return "", errBadPath
	}
	return realPath, nil
}

// errBadPath is deliberately uniform: the caller learns that the path was
// refused, not whether the file exists or where the boundary is.
var errBadPath = errPath{}

type errPath struct{}

func (errPath) Error() string { return "invalid artifact path" }

// runOutputDir resolves an occurrence id to the directory that run wrote into.
func (s *Server) runOutputDir(idStr string) (string, bool) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return "", false
	}
	run, err := s.store.RunByID(id)
	if err != nil || run.OutputDir == "" {
		return "", false
	}
	return run.OutputDir, true
}

// Delivery artifacts.
//
// A Delivery task is git work, so its review is a diff — but a diff is the
// wrong instrument for what a task now produces. An agent that generated a PNG
// or an MP3 into its worktree shows up in the diff as "binary file changed",
// which is true and useless. These two endpoints give that output the same
// treatment Daily's already gets: list it, classify it, serve it back for
// preview.
//
// The worktree is resolved from repo+branch through the same path the diff
// uses, so a caller still names a task and never a directory.

// walkArtifacts lists everything under dir as artifacts, newest first. Shared
// with Daily so both galleries agree on what counts as an artifact and on which
// bookkeeping directories are not output.
func walkArtifacts(dir string) ([]Artifact, error) {
	out := []Artifact{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry should not fail the whole listing
		}
		if d.IsDir() {
			if d.Name() == ".orchestra" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		out = append(out, Artifact{
			Path:    filepath.ToSlash(rel),
			Name:    d.Name(),
			Size:    info.Size(),
			Kind:    artifactKind(d.Name()),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ModTime != out[j].ModTime {
			return out[i].ModTime > out[j].ModTime
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// serveArtifact writes one file back with a content type from the inline
// allowlist, or as a download. Shared with Daily: the rule about what may be
// rendered in the app's own origin must not have two implementations that can
// drift apart.
func serveArtifact(w http.ResponseWriter, r *http.Request, dir, rel string) {
	abs, err := safeJoinDir(dir, rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		writeErr(w, 404, "no such artifact")
		return
	}
	ctype, inlineOK := inlineTypes[strings.ToLower(filepath.Ext(abs))]
	download := r.URL.Query().Get("download") != ""
	// Never let the browser guess a type: sniffing is exactly how a file that is
	// not on the allowlist would end up rendered as a document anyway.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if inlineOK && !download {
		w.Header().Set("Content-Type", ctype)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(abs)))
	}
	http.ServeFile(w, r, abs)
}

// taskWorktree resolves the repo+branch query pair to a worktree directory.
func (s *Server) taskWorktree(r *http.Request) (string, bool) {
	repo, ok := s.repo(r.URL.Query().Get("repo"))
	if !ok {
		return "", false
	}
	wt, err := s.worktreePath(repo, r.URL.Query().Get("branch"))
	if err != nil {
		return "", false
	}
	return wt, true
}

func (s *Server) handleTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.taskWorktree(r)
	if !ok {
		writeErr(w, 404, "unknown task worktree")
		return
	}
	out, err := walkArtifacts(dir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"artifacts": out})
}

func (s *Server) handleTaskArtifact(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.taskWorktree(r)
	if !ok {
		writeErr(w, 404, "unknown task worktree")
		return
	}
	serveArtifact(w, r, dir, r.URL.Query().Get("path"))
}

// handleDailyArtifacts lists what a scheduled run produced.
func (s *Server) handleDailyArtifacts(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.runOutputDir(r.URL.Query().Get("run"))
	if !ok {
		writeErr(w, 404, "unknown run")
		return
	}
	out, err := walkArtifacts(dir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"artifacts": out})
}

// handleDailyArtifact serves one artifact's bytes, for preview or download.
//
// http.ServeFile rather than reading the file into a JSON string: a JSON
// round-trip destroys anything binary, and ServeFile also answers byte ranges,
// without which a <video> can play from the start but cannot seek.
func (s *Server) handleDailyArtifact(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.runOutputDir(r.URL.Query().Get("run"))
	if !ok {
		writeErr(w, 404, "unknown run")
		return
	}
	serveArtifact(w, r, dir, r.URL.Query().Get("path"))
}
