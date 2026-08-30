package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestra/hostagent/internal/store"
)

// Files a schedule hands its own agents.
//
// A Daily run begins in an empty directory. That is right for output — a report,
// a rendered video, a chart — but it left no way to give the run an input. The
// knowledge base is not that: it is a searchable corpus shared across every
// schedule, reached through retrieval and never mounted into a sandbox. What was
// missing is the other kind of input, the one that belongs to this task and is
// simply meant to be on disk when it starts.
//
// The bytes are copied here at attach time rather than the path being
// remembered. A schedule fires unattended, possibly for months; pointing at a
// file on the author's disk would mean a run that breaks when a folder is
// tidied, and worse, one that quietly picks up whatever the file says later. An
// attachment is what was attached.
//
// They are staged into the run's fresh output directory, so each occurrence gets
// its own copy and an agent that rewrites one cannot affect the next run.

// attachMaxBytes bounds one attachment. Matches the ceiling the agent's own
// tools use for a file they upload, since that is where these usually end up.
const attachMaxBytes = 32 << 20

// attachRoot is where a schedule's attachments live, beside the database.
func (s *Server) attachRoot(scheduleID string) string {
	base := s.cfg.DataDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "orchestra-hostagent")
	}
	return filepath.Join(base, "attachments", sanitize(scheduleID))
}

// attachName reduces a submitted filename to a leaf that cannot escape.
//
// filepath.Base first, so a name carrying directories — or "../.." — becomes
// the last element and nothing more. The remaining rejects are the leaves that
// are not names at all. This is a flat directory by design: an attachment is a
// file the run finds beside it, not a tree to reproduce.
func attachName(name string) (string, error) {
	clean := filepath.Base(strings.TrimSpace(strings.ReplaceAll(name, "\\", "/")))
	if clean == "" || clean == "." || clean == ".." || clean == string(filepath.Separator) {
		return "", fmt.Errorf("invalid file name")
	}
	if strings.HasPrefix(clean, ".") {
		// A dotfile is not refused for being hidden but for being invisible in
		// the list the author will later read to decide what a run receives.
		return "", fmt.Errorf("file name must not start with a dot")
	}
	return clean, nil
}

// handleScheduleAttach stores one uploaded file against a schedule.
func (s *Server) handleScheduleAttach(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	id := r.URL.Query().Get("schedule")
	sc, err := s.store.ByID(id)
	if err != nil || sc == nil {
		writeErr(w, 404, "unknown schedule: "+id)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachMaxBytes+(1<<20))
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "expected a multipart form with a `file` part: "+err.Error())
		return
	}
	defer file.Close()
	name, err := attachName(header.Filename)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	dir := s.attachRoot(sc.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, "creating attachment dir: "+err.Error())
		return
	}
	dst, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		writeErr(w, 500, "writing attachment: "+err.Error())
		return
	}
	n, copyErr := io.Copy(dst, io.LimitReader(file, attachMaxBytes+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(filepath.Join(dir, name))
		writeErr(w, 500, "writing attachment failed")
		return
	}
	if n > attachMaxBytes {
		// Removed rather than truncated: half a file on disk is worse than none,
		// because the run would receive it and have no way to know.
		os.Remove(filepath.Join(dir, name))
		writeErr(w, 413, fmt.Sprintf("attachment is over the %d MB limit", attachMaxBytes>>20))
		return
	}

	// Re-attaching under an existing name replaces it, in the list as on disk.
	next := make([]store.Attachment, 0, len(sc.Attachments)+1)
	for _, a := range sc.Attachments {
		if a.Name != name {
			next = append(next, a)
		}
	}
	sc.Attachments = append(next, store.Attachment{Name: name, Size: n, AddedAt: time.Now().UTC().Format(time.RFC3339)})
	updated, err := s.store.Update(sc)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, updated)
}

// handleScheduleDetach removes one attachment, from the list and from disk.
func (s *Server) handleScheduleDetach(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	q := r.URL.Query()
	sc, err := s.store.ByID(q.Get("schedule"))
	if err != nil || sc == nil {
		writeErr(w, 404, "unknown schedule: "+q.Get("schedule"))
		return
	}
	name, err := attachName(q.Get("name"))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	next := make([]store.Attachment, 0, len(sc.Attachments))
	found := false
	for _, a := range sc.Attachments {
		if a.Name == name {
			found = true
			continue
		}
		next = append(next, a)
	}
	if !found {
		writeErr(w, 404, "no such attachment: "+name)
		return
	}
	// The file goes first: a record with no file would stage nothing silently,
	// while a file with no record is inert and cleaned up by the next detach of
	// the same name.
	if err := os.Remove(filepath.Join(s.attachRoot(sc.ID), name)); err != nil && !os.IsNotExist(err) {
		writeErr(w, 500, "removing attachment: "+err.Error())
		return
	}
	sc.Attachments = next
	updated, err := s.store.Update(sc)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, updated)
}

// stageAttachments copies a schedule's files into the directory its run starts
// in, and reports how many landed.
//
// Best effort per file: one unreadable attachment should not stop a run that
// has three others and a task that may not need the fourth. What it must not do
// is fail silently, so every skip is logged with the run it belonged to.
func (s *Server) stageAttachments(sc *store.Schedule, dir string) int {
	staged := 0
	for _, a := range sc.Attachments {
		name, err := attachName(a.Name)
		if err != nil {
			log.Printf("hostagent: schedule %s: skipping attachment %q: %v", sc.ID, a.Name, err)
			continue
		}
		src := filepath.Join(s.attachRoot(sc.ID), name)
		b, err := os.ReadFile(src)
		if err != nil {
			log.Printf("hostagent: schedule %s: attachment %q is recorded but unreadable: %v", sc.ID, name, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			log.Printf("hostagent: schedule %s: staging attachment %q: %v", sc.ID, name, err)
			continue
		}
		staged++
	}
	return staged
}
