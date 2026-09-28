package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"orchestra/hostagent/internal/store"
)

// Chat: a standing conversation with an agent.
//
// Every turn is an ordinary orchestrator run. Nothing here is a new execution
// path — the frontend compiles the bound template exactly as Delivery and Daily
// do (compileRef → buildRunSpec) and hands over the finished run spec; this file
// gives that run somewhere to work, records what was said, and follows the run
// to its end so the transcript is complete whether or not anyone was watching.
//
// Three decisions worth stating, because each of them had an obvious-looking
// alternative:
//
// The conversation gets ONE working directory, shared by every turn. A Daily
// occurrence takes a fresh directory because occurrences are independent; turns
// are not. An agent asked to revise what it wrote last turn has to be able to
// read it, and the artifacts panel lists what the conversation has produced, not
// what its most recent message produced. Per-turn directories would have made
// both of those impossible for the sake of an isolation nobody asked for.
//
// A chat run is ATTENDED, so it does not go through startRun: that function
// forces `unattended`, which restricts a run to images approved for firing with
// nobody watching. Someone is sitting in front of this one, so the restriction
// does not apply and asserting it would silently narrow what a chat may run.
//
// The reply text comes from the run's own handoff manifest rather than from
// parsing logs. The agent runtime writes each stage's closing message there (see
// agent/internal/handoff), written by the runner rather than the model, so "what
// did it answer" is a file on disk and not a guess about which log line was
// prose.

const (
	// chatPollEvery / chatPollFor bound the per-turn watcher. A chat turn that
	// takes longer than this is stuck, and the UI can still read the run's own
	// status from the controller.
	chatPollEvery = 3 * time.Second
	chatPollFor   = 40 * time.Minute
)

// chatRoot is where conversations keep their working directories.
func (s *Server) chatRoot() string {
	if s.cfg.DataDir != "" {
		return filepath.Join(s.cfg.DataDir, "chat")
	}
	return filepath.Join(os.TempDir(), "orchestra-chat")
}

// chatDir is one conversation's working directory. The id is sanitized because
// it becomes a path segment.
func (s *Server) chatDir(convID string) string {
	return filepath.Join(s.chatRoot(), sanitize(convID))
}

// stageManifest is the part of a stage's handoff manifest this service reads.
// The shape belongs to agent/internal/handoff, which is a different Go module —
// so this is a deliberate partial copy, like runOutcome is of the controller's
// run status. Summary is the agent's closing message.
type stageManifest struct {
	Stage   string   `json:"stage"`
	Summary string   `json:"summary"`
	Files   []string `json:"files"`
	Error   string   `json:"error"`
}

// readReply returns the text a finished run should answer with: the named
// stage's manifest summary, or — when the caller did not say which stage is the
// sink, or that stage reported nothing — the most recently written manifest,
// which in a serial run is the last stage to finish.
func readReply(dir, sinkStage string) string {
	stagesDir := filepath.Join(dir, ".orchestra", "stages")
	read := func(path string) (stageManifest, bool) {
		b, err := os.ReadFile(path)
		if err != nil {
			return stageManifest{}, false
		}
		var m stageManifest
		if json.Unmarshal(b, &m) != nil {
			return stageManifest{}, false
		}
		return m, true
	}
	if sinkStage != "" {
		// handoff sanitizes the stage id into the filename; the ids this service
		// sees come from a compiled run spec, so the same replacement suffices.
		name := strings.NewReplacer("/", "-", "..", "-", " ", "-").Replace(sinkStage) + ".json"
		if m, ok := read(filepath.Join(stagesDir, name)); ok && strings.TrimSpace(m.Summary) != "" {
			return m.Summary
		}
	}
	entries, err := os.ReadDir(stagesDir)
	if err != nil {
		return ""
	}
	var newest stageManifest
	var newestAt time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		m, ok := read(filepath.Join(stagesDir, e.Name()))
		if !ok || strings.TrimSpace(m.Summary) == "" {
			continue
		}
		if newestAt.IsZero() || info.ModTime().After(newestAt) {
			newest, newestAt = m, info.ModTime()
		}
	}
	return newest.Summary
}

// estimateTokens is the char/4 estimate the agent runtime falls back to when a
// provider reports no token count. It is used the same way here — to decide
// when a transcript is big enough to compact — so the two must not drift.
func estimateTokens(text string) int { return len([]rune(text)) / 4 }

/* ── conversations ─────────────────────────────────────────────────── */

func (s *Server) handleChatConversations(w http.ResponseWriter, _ *http.Request) {
	if !s.storeReady(w) {
		return
	}
	rows, err := s.store.ChatConversations()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if rows == nil {
		rows = []*store.ChatConversation{}
	}
	writeJSON(w, 200, map[string]any{"conversations": rows})
}

// handleChatConversationSave creates a conversation, or updates one when the
// body carries an id. Title, bound template and scope are all mutable: changing
// the agent mid-conversation is a supported move, and takes effect from the next
// turn rather than rewriting what earlier turns ran as.
func (s *Server) handleChatConversationSave(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct {
		ID            string                `json:"id"`
		Title         string                `json:"title"`
		TemplateRef   string                `json:"templateRef"`
		TemplateLabel string                `json:"templateLabel"`
		Scope         *store.KnowledgeScope `json:"scope"`
	}
	if !decode(w, r, &req) {
		return
	}
	c := &store.ChatConversation{
		ID: req.ID, Title: strings.TrimSpace(req.Title),
		TemplateRef: req.TemplateRef, TemplateLabel: req.TemplateLabel, Scope: req.Scope,
	}
	var out *store.ChatConversation
	var err error
	if req.ID == "" {
		out, err = s.store.CreateChat(c)
	} else {
		out, err = s.store.UpdateChat(c)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if out == nil {
		writeErr(w, 404, "unknown conversation: "+req.ID)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleChatConversationDelete(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	id := r.URL.Query().Get("id")
	// The directory goes with the rows. It holds only what this conversation's
	// runs wrote, and leaving it behind would accumulate agent output nothing can
	// reach any more.
	dir := s.chatDir(id)
	ok, err := s.store.DeleteChat(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, 404, "unknown conversation: "+id)
		return
	}
	if within(dir, s.chatRoot()) {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("hostagent: removing chat dir %s: %v", dir, err)
		}
	}
	writeJSON(w, 200, map[string]string{"deleted": id})
}

// within reports whether path sits under root — a guard on every recursive
// delete, so a malformed id can never turn into a removal outside chatRoot.
func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

/* ── turns ─────────────────────────────────────────────────────────── */

func (s *Server) handleChatTurns(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	id := r.URL.Query().Get("conversation")
	c, err := s.store.ChatByID(id)
	if err != nil || c == nil {
		writeErr(w, 404, "unknown conversation: "+id)
		return
	}
	turns, err := s.store.Turns(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"turns": turns})
}

// handleChatTurnCreate records a user turn and launches the run that answers it.
//
// The run spec arrives compiled: the frontend owns the template stores and the
// history it decided to send, so it is the only component that can build the
// task text, exactly as it is for a schedule. What this adds is everything that
// has to be decided host-side — the working directory, the knowledge scope
// resolved from the graph as it is now, and the fact that a human is watching.
func (s *Server) handleChatTurnCreate(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req struct {
		Conversation string          `json:"conversation"`
		Text         string          `json:"text"`
		Quotes       []string        `json:"quotes"`
		Agent        string          `json:"agent"`
		SinkStage    string          `json:"sinkStage"`
		RunSpec      json.RawMessage `json:"runSpec"`
	}
	if !decode(w, r, &req) {
		return
	}
	c, err := s.store.ChatByID(req.Conversation)
	if err != nil || c == nil {
		writeErr(w, 404, "unknown conversation: "+req.Conversation)
		return
	}
	if len(req.RunSpec) == 0 {
		writeErr(w, 400, "no agent template bound to this conversation")
		return
	}
	dir := s.chatDir(c.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, "creating chat dir: "+err.Error())
		return
	}

	seq, err := s.store.NextTurnSeq(c.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	userTurn := &store.ChatTurn{
		ConversationID: c.ID, Seq: seq, Kind: store.TurnUser,
		Text: req.Text, Quotes: req.Quotes, TokensEst: estimateTokens(req.Text),
	}
	if err := s.store.AddTurn(userTurn); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	agentTurn := &store.ChatTurn{
		ConversationID: c.ID, Seq: seq + 1, Kind: store.TurnAgent,
		Status: "running", Agent: req.Agent, OutputDir: dir,
	}
	if err := s.store.AddTurn(agentTurn); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	// The scope is resolved now, not when it was chosen: the groups under a
	// project change as the graph is edited, and a conversation that meant "this
	// project's knowledge" should follow it.
	groups, scoped := s.scopeGroups(c.Scope)
	if scoped {
		s.syncKnowledgeGroupsLogged("chat " + c.ID + " turn")
	}
	taskID := sanitize(c.ID + "-" + strconv.Itoa(agentTurn.Seq))
	runID, err := s.startChatRun(taskID, dir, req.RunSpec, groups, scoped)
	if err != nil {
		_ = s.store.FinishTurn(agentTurn.ID, "failed", "", 0)
		writeErr(w, 502, "starting run: "+err.Error())
		return
	}
	agentTurn.RunID = runID
	if err := s.store.SetTurnRun(agentTurn.ID, runID); err != nil {
		log.Printf("hostagent: recording chat run %s: %v", runID, err)
	}
	_ = s.store.TouchChat(c.ID)

	go s.watchChatTurn(agentTurn.ID, runID, dir, req.SinkStage)

	writeJSON(w, 201, map[string]any{"userTurn": userTurn, "agentTurn": agentTurn, "runId": runID})
}

// handleChatTurn returns one turn as the store now holds it — what the UI polls
// to learn that a reply has landed. Live stage-by-stage progress comes from the
// controller directly, which is the component that actually knows it.
func (s *Server) handleChatTurn(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad turn id")
		return
	}
	t, err := s.store.TurnByID(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if t == nil {
		writeErr(w, 404, "unknown turn")
		return
	}
	writeJSON(w, 200, t)
}

// watchChatTurn follows a turn's run to its end and writes the reply into the
// transcript. It exists so that closing the window mid-turn does not lose the
// answer: the run is going to finish either way, and a chat whose transcript
// only records what the UI happened to be present for is not a transcript.
func (s *Server) watchChatTurn(turnID int64, runID, dir, sinkStage string) {
	deadline := time.Now().Add(chatPollFor)
	for {
		time.Sleep(chatPollEvery)
		out, err := s.runStatus(runID)
		if err != nil {
			if time.Now().After(deadline) {
				return
			}
			continue
		}
		switch out.Status {
		case "running", "":
			if time.Now().After(deadline) {
				log.Printf("hostagent: chat run %s still running after %s; stopped watching", runID, chatPollFor)
				return
			}
		default:
			reply := readReply(dir, sinkStage)
			status := out.Status
			if status == "done" && strings.TrimSpace(reply) == "" {
				// Every stage exited 0 and none of them said anything. That is not
				// a failure and not an answer either, so it is recorded as its own
				// outcome rather than shown as an empty message.
				status = "empty"
			}
			if err := s.store.FinishTurn(turnID, status, reply, estimateTokens(reply)); err != nil {
				log.Printf("hostagent: recording chat reply for run %s: %v", runID, err)
			}
			return
		}
	}
}

// startChatRun submits a turn's compiled run spec, stating the things a chat run
// must not inherit from whatever the frontend sent.
//
// It is startRun's sibling and deliberately not a call into it: the one value
// that function forces — `unattended` — is the one value a chat must not carry.
// A reviewer is sitting in front of this run, so the image restrictions meant for
// schedules firing unwatched do not apply, and asserting them here would quietly
// forbid a chat from running an image the operator approved for interactive use.
func (s *Server) startChatRun(taskID, dir string, spec json.RawMessage, groups []string, scoped bool) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(spec, &m); err != nil {
		return "", err
	}
	m["taskId"] = taskID
	m["worktreePath"] = dir
	// The network posture, stated rather than inherited. The controller already
	// treats anything but the literal "relaxed" as strict, so an absent value is
	// safe — but "safe because of how the other side parses it" is a property that
	// can be lost in a refactor there, and a chat has no reason to ever ask for
	// the relaxed island. Delivery states it for the same reason.
	m["isolation"] = "strict"
	// A conversation's directory is a plain directory, not a git worktree, so
	// "isolated" is not available to the orchestrator and one shared directory is
	// the only valid arrangement — which in turn means one stage at a time. Stated
	// here rather than trusted from the spec, for the same reason startRun states
	// it: the caller knows what kind of directory this is and the spec does not.
	m["worktreeMode"] = "shared"
	m["maxParallel"] = 1
	delete(m, "unattended")
	if scoped {
		applyStageScopes(m, groups)
		m["groups"] = groups
	} else {
		delete(m, "groups")
	}
	return s.submitRun(m)
}

// writeCapped copies src into path, refusing to keep a file over the attachment
// limit. A partial file is removed rather than left: half an attachment on disk
// is worse than none, because the next turn's agent would receive it with no way
// to know it is truncated.
func writeCapped(path string, src io.Reader) (int64, error) {
	dst, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(dst, io.LimitReader(src, attachMaxBytes+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil || n > attachMaxBytes {
		os.Remove(path)
		if n > attachMaxBytes {
			return n, errTooLarge
		}
		if copyErr != nil {
			return n, copyErr
		}
		return n, closeErr
	}
	return n, nil
}

var errTooLarge = &apiError{msg: "attachment is over the limit"}

/* ── artifacts ─────────────────────────────────────────────────────── */

// The conversation's working directory is its gallery. Both handlers reuse the
// listing and serving rules Daily and Delivery already share, so what counts as
// an artifact and what may be rendered inline has one implementation.

func (s *Server) chatArtifactDir(r *http.Request) (string, bool) {
	id := r.URL.Query().Get("conversation")
	c, err := s.store.ChatByID(id)
	if err != nil || c == nil {
		return "", false
	}
	return s.chatDir(c.ID), true
}

func (s *Server) handleChatArtifacts(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	dir, ok := s.chatArtifactDir(r)
	if !ok {
		writeErr(w, 404, "unknown conversation")
		return
	}
	out, err := walkArtifacts(dir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"artifacts": out})
}

func (s *Server) handleChatArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	dir, ok := s.chatArtifactDir(r)
	if !ok {
		writeErr(w, 404, "unknown conversation")
		return
	}
	serveArtifact(w, r, dir, r.URL.Query().Get("path"))
}

/* ── attachments ───────────────────────────────────────────────────── */

// handleChatAttach writes an uploaded file straight into the conversation's
// working directory.
//
// A schedule stages its attachments into each occurrence's fresh directory
// because that directory does not exist until the schedule fires. A conversation
// has one directory that already exists, so the file simply goes there and is
// visible to the next turn's agent — and to the artifacts panel, which is the
// honest place for it: from the agent's point of view a file someone handed it
// and a file it wrote are both just contents of /work.
func (s *Server) handleChatAttach(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	id := r.URL.Query().Get("conversation")
	c, err := s.store.ChatByID(id)
	if err != nil || c == nil {
		writeErr(w, 404, "unknown conversation: "+id)
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
	dir := s.chatDir(c.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, "creating chat dir: "+err.Error())
		return
	}
	size, err := writeCapped(filepath.Join(dir, name), file)
	if err != nil {
		if size > attachMaxBytes {
			writeErr(w, 413, "attachment is over the limit")
			return
		}
		writeErr(w, 500, "writing attachment failed")
		return
	}
	writeJSON(w, 200, map[string]any{"name": name, "size": size})
}
