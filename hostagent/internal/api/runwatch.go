package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"orchestra/hostagent/internal/store"
)

// Following a scheduled run to its end.
//
// An occurrence used to be written the moment its run was submitted, with the
// status 'executed', and never revisited. That made the Daily history a record
// of what had been *started* rather than what had *happened*: a run whose first
// stage could not launch, and a run where every stage exited 0 and wrote
// nothing, both appeared as a schedule that had run. Neither is something the
// operator would call a success, and neither was distinguishable from one.
//
// So a launched run is watched until it is terminal, and the occurrence is
// rewritten with what it turned into. The watcher is deliberately dumb: it
// polls, it gives up after a bound, and losing it (a restart, say) leaves the
// occurrence at 'executed', which is exactly what it used to be.

const (
	runPollEvery = 10 * time.Second
	// A video generation can legitimately take many minutes; well past that,
	// the run is stuck rather than slow, and a watcher that never exits is a
	// goroutine leak per schedule tick.
	runPollFor = 2 * time.Hour
)

// runOutcome is the part of the controller's run status this service reads.
type runOutcome struct {
	Status    string   `json:"status"`
	Artifacts []string `json:"artifacts"`
}

// watchRun follows a run to completion and records its outcome. Runs in its own
// goroutine; every exit path is silent except a genuine failure to record.
func (s *Server) watchRun(runID string) {
	deadline := time.Now().Add(runPollFor)
	for {
		time.Sleep(runPollEvery)
		out, err := s.runStatus(runID)
		if err != nil {
			// The controller may be restarting; keep trying until the deadline.
			if time.Now().After(deadline) {
				return
			}
			continue
		}
		switch out.Status {
		case "running", "":
			if time.Now().After(deadline) {
				log.Printf("hostagent: run %s still running after %s; stopped watching", runID, runPollFor)
				return
			}
		case "done":
			// Nothing failed. Whether that is a success depends on whether it
			// made anything, which the stages themselves reported.
			status := store.RunStatusDone
			if len(out.Artifacts) == 0 {
				status = store.RunStatusEmpty
				log.Printf("hostagent: run %s completed without producing any files", runID)
			}
			s.recordOutcome(runID, status)
			return
		default: // failed, stopped
			s.recordOutcome(runID, store.RunStatusFailed)
			return
		}
	}
}

func (s *Server) recordOutcome(runID, status string) {
	if err := s.store.SetOccurrenceOutcome(runID, status); err != nil {
		log.Printf("hostagent: recording outcome for run %s: %v", runID, err)
	}
}

func (s *Server) runStatus(runID string) (runOutcome, error) {
	var out runOutcome
	u := strings.TrimRight(s.cfg.sandboxURL(), "/") + "/run?id=" + url.QueryEscape(runID)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(u)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		return out, errStatus(resp.StatusCode)
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

type errStatus int

func (e errStatus) Error() string { return fmt.Sprintf("run status: HTTP %d", int(e)) }
