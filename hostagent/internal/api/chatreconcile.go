package api

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// Settling chat turns that were left in flight.
//
// A turn is finished by watchChatTurn, a goroutine that polls the run until it
// is terminal and then writes the reply into the transcript. That goroutine
// lives in this process, so quitting the app — or replacing it, which is the
// same thing from in here — kills it, and the row stays "running" with nobody
// left to change it. The conversation then reads as permanently busy: a chat
// that says it is working on something nothing is working on.
//
// Nothing detected that before. It is not a crash and not an error; the state is
// simply never revisited. So it is revisited here, once, at startup.
//
// The controller is the authority on whether a run is still going, and it is a
// separate process that may still be coming up — so this waits for it rather
// than assuming its silence means the run is gone. If it never answers, the
// turns are left exactly as they were: unchanged is the state they are already
// in, and guessing "failed" for a run that is quietly still going would be
// worse than a stale label.

// How long to wait for the controller before giving up on reconciling. The
// sidecars start together, so this is about startup order, not about a
// controller that is down. Vars only so tests can shorten the wait; production
// never changes them.
var (
	reconcileWaitFor   = 90 * time.Second
	reconcileWaitEvery = 3 * time.Second
)

// reconcileChatTurns settles every turn left marked running. Runs in its own
// goroutine at startup.
func (s *Server) reconcileChatTurns() {
	if s.store == nil {
		return
	}
	turns, err := s.store.RunningTurns()
	if err != nil {
		log.Printf("hostagent: reading turns left in flight: %v", err)
		return
	}
	if len(turns) == 0 {
		return
	}
	if !s.waitForController() {
		log.Printf("hostagent: %d chat turn(s) left in flight, and the sandbox controller did not answer; leaving them alone", len(turns))
		return
	}

	for _, t := range turns {
		if t.RunID == "" {
			// Recorded, then the submission failed before a run id came back.
			// There is nothing to ask about and nothing to wait for.
			s.settleTurn(t.ID, "failed", "", "no run was ever started")
			continue
		}

		out, err := s.runStatus(t.RunID)
		switch {
		case err != nil:
			// The controller keeps its runs in memory, so a restart loses them:
			// "unknown run" is the ordinary answer here, not an anomaly. The run
			// is over — its container went with the app — and whatever the stage
			// managed to write is still on disk.
			s.settleTurn(t.ID, "stopped", t.OutputDir, "the app restarted while this turn was running")
		case out.Status == "running":
			// Still going, against expectation — pick the watch back up so it
			// finishes properly rather than being settled out from under itself.
			log.Printf("hostagent: chat turn %d is still running; resuming the watch", t.ID)
			go s.watchChatTurn(t.ID, t.RunID, t.OutputDir, "")
		case out.Status == "done":
			s.settleTurn(t.ID, "done", t.OutputDir, "")
		default:
			s.settleTurn(t.ID, out.Status, t.OutputDir, "")
		}
	}
}

// settleTurn writes a final status, carrying over whatever the stage had written
// before it stopped. A turn that reasoned for four minutes and wrote a file said
// something; losing that because the run did not reach its own end is the part
// worth avoiding.
func (s *Server) settleTurn(turnID int64, status, dir, note string) {
	reply := ""
	if dir != "" {
		reply = readReply(dir, "")
	}
	if strings.TrimSpace(reply) == "" && status == "done" {
		// Finished and said nothing: a different fact from failing, and one the
		// UI has its own word for.
		status = "empty"
	}
	if err := s.store.FinishTurn(turnID, status, reply, estimateTokens(reply)); err != nil {
		log.Printf("hostagent: settling chat turn %d: %v", turnID, err)
		return
	}
	if note != "" {
		log.Printf("hostagent: chat turn %d settled as %s (%s)", turnID, status, note)
	}
}

// waitForController blocks until the sandbox controller answers /health, or the
// deadline passes. The bool says which.
func (s *Server) waitForController() bool {
	url := strings.TrimRight(s.cfg.sandboxURL(), "/") + "/health"
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(reconcileWaitFor)
	for {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(reconcileWaitEvery)
	}
}
