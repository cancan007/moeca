package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestra/hostagent/internal/store"
)

// A turn left in flight is settled at startup.
//
// The goroutine that finishes a turn dies with the process, so quitting the app
// mid-turn — or replacing it, which is the same thing from in here — used to
// leave the row saying "running" forever, and the conversation read as
// permanently busy. Nothing failed and nothing was logged; the state was simply
// never revisited.

// strandedTurn records a conversation with one agent turn stuck at running.
func strandedTurn(t *testing.T, s *Server, runID, dir string) *store.ChatTurn {
	t.Helper()
	c, err := s.store.CreateChat(&store.ChatConversation{Title: "left in flight"})
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := s.store.NextTurnSeq(c.ID)
	turn := &store.ChatTurn{
		ConversationID: c.ID, Seq: seq, Kind: store.TurnAgent,
		Status: "running", RunID: runID, OutputDir: dir, Agent: "Builder",
	}
	if err := s.store.AddTurn(turn); err != nil {
		t.Fatal(err)
	}
	return turn
}

// writeManifest puts a stage's account of itself on disk, the way a run does.
func writeManifest(t *testing.T, dir, stage, summary string) {
	t.Helper()
	stages := filepath.Join(dir, ".orchestra", "stages")
	if err := os.MkdirAll(stages, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"stage": stage, "summary": summary})
	if err := os.WriteFile(filepath.Join(stages, stage+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The controller keeps runs in memory, so after a restart it has never heard of
// them. That is the ordinary case, not an anomaly — and whatever the stage wrote
// before it died is still on disk and still the best account of the turn.
func TestReconcileSettlesATurnTheControllerHasForgotten(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, `{"error":"unknown run"}`, 404)
	})
	dir := t.TempDir()
	writeManifest(t, dir, "builder", "I had got as far as the retry table.")
	turn := strandedTurn(t, s, "run-gone", dir)

	s.reconcileChatTurns()

	got, err := s.store.TurnByID(turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "stopped" {
		t.Errorf("status = %q, want stopped", got.Status)
	}
	if !strings.Contains(got.Text, "retry table") {
		t.Errorf("text = %q; the partial answer on disk was thrown away", got.Text)
	}
	if got.TokensEst == 0 {
		t.Error("the recovered reply was not counted toward the context")
	}
}

// A run that really is still going is picked back up, not settled out from under
// itself.
func TestReconcileResumesARunStillGoing(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.Write([]byte(`{"status":"running"}`))
	})
	turn := strandedTurn(t, s, "run-live", t.TempDir())

	s.reconcileChatTurns()

	got, _ := s.store.TurnByID(turn.ID)
	if got.Status != "running" {
		t.Errorf("status = %q, want it left running", got.Status)
	}
}

// A finished run that produced no closing message is empty, not failed: it ran,
// it just did not say anything.
func TestReconcileMarksASilentFinishEmpty(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.Write([]byte(`{"status":"done"}`))
	})
	turn := strandedTurn(t, s, "run-quiet", t.TempDir())

	s.reconcileChatTurns()

	got, _ := s.store.TurnByID(turn.ID)
	if got.Status != "empty" {
		t.Errorf("status = %q, want empty", got.Status)
	}
}

// A turn whose run never started has nothing to ask about.
func TestReconcileSettlesATurnThatNeverLaunched(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	})
	turn := strandedTurn(t, s, "", "")

	s.reconcileChatTurns()

	got, _ := s.store.TurnByID(turn.ID)
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
}

// swapReconcileWait shortens the controller wait for a test, returning the undo.
func swapReconcileWait(t *testing.T, d time.Duration) func() {
	t.Helper()
	oldFor, oldEvery := reconcileWaitFor, reconcileWaitEvery
	reconcileWaitFor, reconcileWaitEvery = d, d
	return func() { reconcileWaitFor, reconcileWaitEvery = oldFor, oldEvery }
}

// If the controller cannot be reached at all, the turns are left exactly as they
// were. Guessing "failed" for a run that is quietly still going would be worse
// than a stale label, and unchanged is the state they are already in.
func TestReconcileLeavesTurnsAloneWithoutAController(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // nothing is listening
	s := New(&Config{NoSeed: true, DataDir: t.TempDir(), Sandbox: SandboxConfig{URL: srv.URL}})
	turn := strandedTurn(t, s, "run-unknown", t.TempDir())

	// Shorten the wait: the point is the decision, not the ninety seconds.
	defer swapReconcileWait(t, 0)()
	s.reconcileChatTurns()

	got, _ := s.store.TurnByID(turn.ID)
	if got.Status != "running" {
		t.Errorf("status = %q, want it left untouched", got.Status)
	}
}
