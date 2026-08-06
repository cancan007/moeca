package api

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// Stage-log archive.
//
// /run/logs reads a stage's output with `docker logs <container>`, which means
// the log lives only as long as the container. Containers are removed when a run
// is cleared, when a name is reused, or by any ordinary `docker system prune` —
// and at that point the record of what the agent actually did is gone, whether
// the run succeeded or failed.
//
// So each stage's output is archived to disk the moment it reaches a terminal
// state, and /run/logs falls back to the archive when the container no longer
// answers. The container remains the source of truth while it exists, so a
// running stage still streams live output.
//
// Caveat: archiving happens when the stage's Wait returns. If the controller
// itself dies mid-stage, that stage's log is only whatever the container still
// holds.

// stageLogPath is where one stage's archived output lives. Both ids are
// sanitized because they reach us from request bodies and become path segments.
func (s *Server) stageLogPath(runID, stageID string) string {
	return filepath.Join(s.cfg.logDir(), sanitizeID(runID), sanitizeID(stageID)+".log")
}

// archiveStageLog captures a finished stage's container output and writes it to
// the archive. Best-effort: a failure here must never fail the run, but it is
// logged, because silently losing the log is exactly the outcome this exists to
// prevent.
func (s *Server) archiveStageLog(runID, stageID, containerID string) {
	if containerID == "" || runID == "" || stageID == "" {
		return
	}
	out, err := s.docker.Logs(containerID)
	if err != nil {
		log.Printf("sandbox: archiving %s/%s: reading container logs: %v", runID, stageID, err)
		return
	}
	path := s.stageLogPath(runID, stageID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("sandbox: archiving %s/%s: %v", runID, stageID, err)
		return
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		log.Printf("sandbox: archiving %s/%s: %v", runID, stageID, err)
	}
}

// readArchivedStageLog returns a stage's archived output, if any.
func (s *Server) readArchivedStageLog(runID, stageID string) (string, bool) {
	b, err := os.ReadFile(s.stageLogPath(runID, stageID))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// stageLog returns a stage's output, preferring the live container and falling
// back to the archive once the container is gone.
func (s *Server) stageLog(runID, stageID, containerID string) (string, bool) {
	if containerID != "" {
		if out, err := s.docker.Logs(containerID); err == nil {
			return out, true
		}
	}
	return s.readArchivedStageLog(runID, stageID)
}

// Note: removing a run deliberately does NOT delete its archive. Clearing a run
// drops its in-memory state and its containers; the log is what remains to look
// back at, which is the whole reason this archive exists. Retention/pruning is
// therefore not handled here.

// Run metadata.
//
// The run table lives in memory, so a restart loses which stages ran, what they
// produced, and the commits that recorded them — while their logs, and the
// commits themselves, survive on disk. That asymmetry meant a past run could
// show its logs but none of its artifacts.
//
// The run's exported state is archived next to its logs and the status endpoint
// falls back to it. Marshalling the Run writes exactly its client-facing shape;
// the mutex and internal indexes are unexported and stay out of it.

// runMetaPath is where one run's archived state lives.
func (s *Server) runMetaPath(runID string) string {
	return filepath.Join(s.cfg.logDir(), sanitizeID(runID), "run.json")
}

// archiveRun writes the run's current state. Called as stages complete, not only
// at the end, so a controller that dies mid-run still leaves the progress it
// had. Best-effort but logged — silently losing this is what it exists to
// prevent.
func (s *Server) archiveRun(run *Run) {
	run.mu.Lock()
	raw, err := json.Marshal(run)
	run.mu.Unlock()
	if err != nil {
		log.Printf("sandbox: archiving run %s: %v", run.ID, err)
		return
	}
	path := s.runMetaPath(run.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		log.Printf("sandbox: archiving run %s: %v", run.ID, err)
		return
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		log.Printf("sandbox: archiving run %s: %v", run.ID, err)
	}
}

// readArchivedRun returns a run's archived state, if any. It is a serving-only
// snapshot — it carries no live scheduling state, so it must never be put back
// into the run table.
func (s *Server) readArchivedRun(runID string) (json.RawMessage, bool) {
	b, err := os.ReadFile(s.runMetaPath(runID))
	if err != nil {
		return nil, false
	}
	return json.RawMessage(b), true
}

// archivedStageIDs lists a run's stages in order, from the archive. This is what
// lets archived logs be served at all: /run/logs walks the run's stages, and
// without the run in memory there is otherwise nothing to walk — the logs would
// sit on disk unreachable.
func (s *Server) archivedStageIDs(runID string) ([]string, bool) {
	raw, ok := s.readArchivedRun(runID)
	if !ok {
		return nil, false
	}
	var meta struct {
		Stages []struct {
			ID string `json:"id"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, false
	}
	ids := make([]string, 0, len(meta.Stages))
	for _, st := range meta.Stages {
		ids = append(ids, st.ID)
	}
	return ids, true
}
