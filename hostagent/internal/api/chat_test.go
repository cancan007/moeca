package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/hostagent/internal/store"
)

// chatServer builds a host agent whose chat runs write into a temp data dir and
// whose orchestrator calls go to h.
func chatServer(t *testing.T, h http.HandlerFunc) *Server {
	t.Helper()
	sb := httptest.NewServer(h)
	t.Cleanup(sb.Close)
	return New(&Config{NoSeed: true, DataDir: t.TempDir(), Sandbox: SandboxConfig{URL: sb.URL}})
}

// runRecorder answers /run and captures what was submitted.
func runRecorder(body *map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/run" {
			json.NewDecoder(r.Body).Decode(body)
			w.WriteHeader(201)
			w.Write([]byte(`{"runId":"chat-run-1"}`))
			return
		}
		// Every poll after the run is submitted: report it still going, so the
		// watcher goroutine neither finishes the turn nor spins.
		w.Write([]byte(`{"status":"running"}`))
	}
}

func newChat(t *testing.T, s *Server) *store.ChatConversation {
	t.Helper()
	c, err := s.store.CreateChat(&store.ChatConversation{
		Title: "retry policy", TemplateRef: "solo:builder", TemplateLabel: "Solo — Builder",
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func postTurn(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/chat/turn", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleChatTurnCreate(rec, req)
	return rec
}

// A chat turn is watched by a human, so it must NOT carry `unattended` — that
// flag narrows the run to images approved for firing with nobody present, and
// asserting it here would silently forbid a conversation from using an image the
// operator approved for interactive work. This is the one property that
// distinguishes a chat run from a scheduled one, so it is pinned by a test.
func TestChatRunIsAttended(t *testing.T) {
	var runBody map[string]any
	s := chatServer(t, runRecorder(&runBody))
	c := newChat(t, s)

	rec := postTurn(t, s, `{"conversation":"`+c.ID+`","text":"hello","agent":"Builder",
		"sinkStage":"builder","runSpec":{"stages":[{"id":"builder","name":"Builder"}],"unattended":true,"maxParallel":3}}`)
	if rec.Code != 201 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if runBody == nil {
		t.Fatal("orchestrator /run was not called")
	}
	if _, ok := runBody["unattended"]; ok {
		t.Errorf("chat run carries unattended: %v — a reviewer is watching it", runBody["unattended"])
	}
	// A conversation's directory is plain, not a git worktree, so one shared
	// directory and one stage at a time is the only valid arrangement.
	// A chat never wants the relaxed network island, and says so rather than
	// relying on the controller reading an absent value as strict.
	if runBody["isolation"] != "strict" {
		t.Errorf("isolation = %v, want strict", runBody["isolation"])
	}
	if _, ok := runBody["delegation"]; ok && runBody["delegation"] == true {
		t.Error("chat run enables sub-agent delegation; nothing asked for it")
	}
	if runBody["worktreeMode"] != "shared" {
		t.Errorf("worktreeMode = %v, want shared", runBody["worktreeMode"])
	}
	if runBody["maxParallel"] != float64(1) {
		t.Errorf("maxParallel = %v, want 1 (shared directory)", runBody["maxParallel"])
	}
	wt, _ := runBody["worktreePath"].(string)
	if !strings.Contains(filepath.ToSlash(wt), "/chat/") {
		t.Errorf("worktreePath = %q, want a chat working directory", wt)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		t.Errorf("working directory was not created at %q: %v", wt, err)
	}
}

// Every turn of a conversation shares one directory: an agent asked to revise
// what it wrote last turn has to be able to read it.
func TestChatTurnsShareOneDirectory(t *testing.T) {
	var runBody map[string]any
	s := chatServer(t, runRecorder(&runBody))
	c := newChat(t, s)
	spec := `{"conversation":"` + c.ID + `","text":"go","runSpec":{"stages":[{"id":"a"}]}}`

	postTurn(t, s, spec)
	first, _ := runBody["worktreePath"].(string)
	postTurn(t, s, spec)
	second, _ := runBody["worktreePath"].(string)

	if first == "" || first != second {
		t.Errorf("turn directories = %q and %q, want one shared directory", first, second)
	}
}

// A user turn and the agent turn answering it are both recorded before the run
// is submitted, so a reply that arrives while the window is closed still has a
// row to land in.
func TestChatTurnRecordsBothSides(t *testing.T) {
	var runBody map[string]any
	s := chatServer(t, runRecorder(&runBody))
	c := newChat(t, s)

	postTurn(t, s, `{"conversation":"`+c.ID+`","text":"why is it slow?","agent":"Builder","runSpec":{"stages":[{"id":"a"}]}}`)

	turns, err := s.store.Turns(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2 (question + answer placeholder)", len(turns))
	}
	if turns[0].Kind != store.TurnUser || turns[0].Text != "why is it slow?" {
		t.Errorf("first turn = %+v, want the user's question", turns[0])
	}
	if turns[1].Kind != store.TurnAgent || turns[1].Status != "running" || turns[1].RunID != "chat-run-1" {
		t.Errorf("second turn = %+v, want a running agent turn carrying the run id", turns[1])
	}
	if turns[0].TokensEst == 0 {
		t.Error("user turn has no token estimate; the context ring has nothing to read")
	}
}

// A conversation with no template bound has nothing to run, and saying so beats
// submitting an empty DAG the controller would refuse.
func TestChatTurnWithoutRunSpecIsRefused(t *testing.T) {
	called := false
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	c := newChat(t, s)

	rec := postTurn(t, s, `{"conversation":"`+c.ID+`","text":"hi"}`)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if called {
		t.Error("orchestrator was called with no run spec")
	}
	if turns, _ := s.store.Turns(c.ID); len(turns) != 0 {
		t.Errorf("turns = %+v, want none recorded for a refused send", turns)
	}
}

// The reply is read from the run's handoff manifest — the file the agent runtime
// writes with each stage's closing message — and the named sink wins over any
// other stage that also reported.
func TestReadReplyPrefersTheSinkStage(t *testing.T) {
	dir := t.TempDir()
	stages := filepath.Join(dir, ".orchestra", "stages")
	if err := os.MkdirAll(stages, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, summary string) {
		b, _ := json.Marshal(map[string]any{"stage": name, "summary": summary})
		if err := os.WriteFile(filepath.Join(stages, name+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("planner", "here is the plan")
	write("reviewer", "looks good, shipped")

	if got := readReply(dir, "reviewer"); got != "looks good, shipped" {
		t.Errorf("readReply(sink) = %q, want the sink's summary", got)
	}
	// With no sink named, the most recently written manifest stands in — in a
	// serial run that is the last stage to finish.
	if got := readReply(dir, ""); got == "" {
		t.Error("readReply with no sink returned nothing; want a fallback summary")
	}
	// A stage that reported nothing must not silently become the answer.
	write("empty", "")
	if got := readReply(dir, "empty"); got == "" {
		t.Error("readReply fell through to nothing when the sink was silent; want the fallback")
	}
}

// Deleting a conversation takes its directory with it: it holds only what this
// conversation's runs wrote, and nothing else can reach it afterwards.
func TestChatDeleteRemovesTheDirectory(t *testing.T) {
	var runBody map[string]any
	s := chatServer(t, runRecorder(&runBody))
	c := newChat(t, s)
	postTurn(t, s, `{"conversation":"`+c.ID+`","text":"go","runSpec":{"stages":[{"id":"a"}]}}`)
	dir, _ := runBody["worktreePath"].(string)
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/chat/conversation?id="+c.ID, nil)
	rec := httptest.NewRecorder()
	s.handleChatConversationDelete(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("working directory survived the delete: %v", err)
	}
	// The turns go with it (foreign key cascade).
	if turns, _ := s.store.Turns(c.ID); len(turns) != 0 {
		t.Errorf("turns = %+v, want none after the conversation was deleted", turns)
	}
}

// The artifacts panel lists the conversation's directory, with the run's own
// bookkeeping excluded — the same rule Daily and Delivery apply.
func TestChatArtifactsSkipBookkeeping(t *testing.T) {
	var runBody map[string]any
	s := chatServer(t, runRecorder(&runBody))
	c := newChat(t, s)
	postTurn(t, s, `{"conversation":"`+c.ID+`","text":"go","runSpec":{"stages":[{"id":"a"}]}}`)
	dir, _ := runBody["worktreePath"].(string)
	os.MkdirAll(filepath.Join(dir, ".orchestra", "stages"), 0o755)
	os.WriteFile(filepath.Join(dir, ".orchestra", "stages", "a.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "chart.png"), []byte("x"), 0o644)

	req := httptest.NewRequest("GET", "/chat/artifacts?conversation="+c.ID, nil)
	rec := httptest.NewRecorder()
	s.handleChatArtifacts(rec, req)

	var out struct{ Artifacts []Artifact }
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Name != "chart.png" {
		t.Errorf("artifacts = %+v, want only chart.png", out.Artifacts)
	}
}
