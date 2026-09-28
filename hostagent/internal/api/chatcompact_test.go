package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"orchestra/hostagent/internal/store"
)

// seedExchanges fills a conversation with n question/answer pairs.
func seedExchanges(t *testing.T, s *Server, convID string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		seq, err := s.store.NextTurnSeq(convID)
		if err != nil {
			t.Fatal(err)
		}
		q := "question " + strconv.Itoa(i)
		a := "answer " + strconv.Itoa(i)
		if err := s.store.AddTurn(&store.ChatTurn{
			ConversationID: convID, Seq: seq, Kind: store.TurnUser, Text: q, TokensEst: estimateTokens(q),
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.store.AddTurn(&store.ChatTurn{
			ConversationID: convID, Seq: seq + 1, Kind: store.TurnAgent, Text: a,
			Status: "done", TokensEst: estimateTokens(a),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func compact(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/chat/compact", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleChatCompact(rec, req)
	return rec
}

// The cut is counted in EXCHANGES and never splits a question from its answer.
func TestCutIndexKeepsWholeExchanges(t *testing.T) {
	var live []store.ChatTurn
	for i := 0; i < 5; i++ {
		live = append(live,
			store.ChatTurn{Seq: i * 2, Kind: store.TurnUser},
			store.ChatTurn{Seq: i*2 + 1, Kind: store.TurnAgent})
	}
	// Keeping 2 exchanges leaves the last four messages; the cut therefore falls
	// after the 3rd exchange, on an answer.
	cut := cutIndex(live, 2)
	if cut != 6 {
		t.Fatalf("cutIndex = %d, want 6", cut)
	}
	if live[cut].Kind != store.TurnUser {
		t.Errorf("the retained tail starts on %s; it must start on a question", live[cut].Kind)
	}
	// Asking to keep more exchanges than exist compacts nothing rather than
	// cutting into the only history there is.
	if got := cutIndex(live, 9); got != 0 {
		t.Errorf("cutIndex(keep>len) = %d, want 0", got)
	}
}

// "recent" mode is the free path: old turns stop being sent, no model is called,
// and the text stays on disk.
func TestCompactRecentDropsWithoutCallingAModel(t *testing.T) {
	called := false
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	c := newChat(t, s)
	seedExchanges(t, s, c.ID, 6)

	rec := compact(t, s, `{"conversation":"`+c.ID+`","mode":"recent","keepTurns":2}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if called {
		t.Error("recent mode reached the gateway; it must cost nothing")
	}

	live, _ := s.store.LiveTurns(c.ID)
	if len(live) != 4 {
		t.Errorf("live turns = %d, want 4 (2 exchanges kept)", len(live))
	}
	// Dropped is a mark, not a delete: the whole transcript is still readable.
	all, _ := s.store.Turns(c.ID)
	if len(all) != 12 {
		t.Errorf("stored turns = %d, want all 12 still present", len(all))
	}
	for _, turn := range all {
		if turn.Dropped && turn.Text == "" {
			t.Error("a dropped turn lost its text; compaction must stay reviewable")
		}
	}
	// No summary is written in this mode — there is nothing to show but what was
	// already said.
	for _, turn := range all {
		if turn.Kind == store.TurnSummary {
			t.Error("recent mode wrote a summary turn")
		}
	}
}

// "sum" mode replaces the middle with a briefing, which reads at the position of
// the range it covers rather than at the end of the conversation.
func TestCompactSumInsertsSummaryInPlace(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/messages") {
			w.Write([]byte(`{"content":[{"type":"text","text":"They want retries with backoff."}]}`))
			return
		}
		w.WriteHeader(404)
	})
	s.cfg.Gateway.URL = strings.TrimSuffix(s.cfg.sandboxURL(), "/")
	c := newChat(t, s)
	seedExchanges(t, s, c.ID, 5)

	rec := compact(t, s, `{"conversation":"`+c.ID+`","mode":"sum","keepTurns":2,"model":"claude-haiku-4-5-20251001","prefix":"/anthropic/"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var out struct {
		Compacted      bool   `json:"compacted"`
		ReplacedTokens int    `json:"replacedTokens"`
		CoversFrom     int    `json:"coversFrom"`
		CoversTo       int    `json:"coversTo"`
		Mode           string `json:"mode"`
	}
	json.NewDecoder(rec.Body).Decode(&out)
	if !out.Compacted || out.ReplacedTokens == 0 {
		t.Errorf("result = %+v, want a compaction that reports what it replaced", out)
	}

	all, _ := s.store.Turns(c.ID)
	summaryAt := -1
	for i, turn := range all {
		if turn.Kind == store.TurnSummary {
			summaryAt = i
			if turn.Text != "They want retries with backoff." {
				t.Errorf("summary text = %q", turn.Text)
			}
			if turn.CoversFrom != out.CoversFrom || turn.CoversTo != out.CoversTo {
				t.Errorf("summary covers %d..%d, response said %d..%d",
					turn.CoversFrom, turn.CoversTo, out.CoversFrom, out.CoversTo)
			}
		}
	}
	if summaryAt == -1 {
		t.Fatal("no summary turn was written")
	}
	// It reads after everything it replaced and before everything it did not.
	for i, turn := range all {
		if turn.Kind == store.TurnSummary {
			continue
		}
		if i < summaryAt && !turn.Dropped {
			t.Errorf("turn %d before the summary is still live; the briefing must follow what it replaced", i)
		}
		if i > summaryAt && turn.Dropped {
			t.Errorf("turn %d after the summary is dropped; the tail must stay verbatim", i)
		}
	}
}

// If the summarizer fails, the transcript is left exactly as it was. Dropping
// turns we could not summarize would lose the conversation to save tokens.
func TestCompactLeavesHistoryAloneWhenSummarizingFails(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream is down", 500)
	})
	s.cfg.Gateway.URL = strings.TrimSuffix(s.cfg.sandboxURL(), "/")
	c := newChat(t, s)
	seedExchanges(t, s, c.ID, 5)

	rec := compact(t, s, `{"conversation":"`+c.ID+`","mode":"sum","keepTurns":2,"model":"m","prefix":"/anthropic/"}`)
	if rec.Code != 502 {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	live, _ := s.store.LiveTurns(c.ID)
	if len(live) != 10 {
		t.Errorf("live turns = %d, want all 10 — a failed summary must change nothing", len(live))
	}
}

// Without a gateway there is nothing to summarize with, and the request is
// refused rather than quietly downgraded to dropping turns.
func TestCompactSumWithoutGatewayIsRefused(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {})
	s.cfg.Gateway.URL = ""
	c := newChat(t, s)
	seedExchanges(t, s, c.ID, 5)

	rec := compact(t, s, `{"conversation":"`+c.ID+`","mode":"sum","keepTurns":2}`)
	if rec.Code != 503 {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if live, _ := s.store.LiveTurns(c.ID); len(live) != 10 {
		t.Errorf("live turns = %d, want all 10", len(live))
	}
}

// The shape where addressing turns by seq range goes wrong.
//
// A briefing's seq is the next free one at the time it was written, so it sits
// ABOVE the turns it covers and can sit above the turns that come after it too.
// When a later compaction replaces only turns whose seqs are below that briefing,
// a range never reaches the briefing — and two briefings covering overlapping
// history both stay live, sending the same conversation to the model twice.
func TestCompactFoldsAnEarlierSummaryOutsideTheRange(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/messages") {
			w.Write([]byte(`{"content":[{"type":"text","text":"briefing"}]}`))
			return
		}
		w.WriteHeader(404)
	})
	s.cfg.Gateway.URL = strings.TrimSuffix(s.cfg.sandboxURL(), "/")
	c := newChat(t, s)

	// First: 5 exchanges (seqs 1..10) compacted keeping 2, so the briefing takes
	// seq 11 while covering 1..6.
	seedExchanges(t, s, c.ID, 5)
	compact(t, s, `{"conversation":"`+c.ID+`","mode":"sum","keepTurns":2,"model":"m","prefix":"/anthropic/"}`)

	// Then 4 more exchanges, seqs 12..19 — all ABOVE the briefing's own seq.
	seedExchanges(t, s, c.ID, 4)

	// Keeping 4 exchanges leaves the new ones verbatim, so what gets replaced is
	// the briefing plus turns 7..10 — a set whose seq range (7..10) excludes the
	// briefing at 11.
	if rec := compact(t, s, `{"conversation":"`+c.ID+`","mode":"sum","keepTurns":4,"model":"m","prefix":"/anthropic/"}`); rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}

	live, _ := s.store.LiveTurns(c.ID)
	summaries := 0
	for _, turn := range live {
		if turn.Kind == store.TurnSummary {
			summaries++
		}
	}
	if summaries != 1 {
		seqs := make([]string, 0, len(live))
		for _, turn := range live {
			seqs = append(seqs, fmt.Sprintf("%s#%d", turn.Kind, turn.Seq))
		}
		t.Errorf("live summaries = %d in %v, want 1 — the earlier briefing must stop being sent", summaries, seqs)
	}
	for _, turn := range live {
		if turn.Kind != store.TurnSummary && turn.Seq <= 10 {
			t.Errorf("turn #%d should have been replaced", turn.Seq)
		}
	}
}

// A mode nobody implemented is refused rather than guessed at.
func TestCompactRejectsUnknownMode(t *testing.T) {
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {})
	c := newChat(t, s)
	if rec := compact(t, s, `{"conversation":"`+c.ID+`","mode":"magic"}`); rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// The briefing's budget is proportional to what it replaces and capped, so a
// short compaction is not asked for an essay and a long one cannot become its
// own context problem.
func TestSummaryBudgetIsProportionalAndCapped(t *testing.T) {
	if got := summaryBudget(120); got != 256 {
		t.Errorf("summaryBudget(120) = %d, want the 256 floor", got)
	}
	if got := summaryBudget(12000); got != 1000 {
		t.Errorf("summaryBudget(12000) = %d, want 1000", got)
	}
	if got := summaryBudget(10_000_000); got != summaryMaxBudget {
		t.Errorf("summaryBudget(huge) = %d, want the %d cap", got, summaryMaxBudget)
	}
}

// A second compaction folds the earlier briefing in rather than sending it twice.
func TestCompactFoldsAnEarlierSummary(t *testing.T) {
	var asked []string
	s := chatServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/messages") {
			var req struct {
				Messages []struct {
					Content []struct{ Text string } `json:"content"`
				} `json:"messages"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if len(req.Messages) > 0 && len(req.Messages[0].Content) > 0 {
				asked = append(asked, req.Messages[0].Content[0].Text)
			}
			w.Write([]byte(`{"content":[{"type":"text","text":"briefing"}]}`))
			return
		}
		w.WriteHeader(404)
	})
	s.cfg.Gateway.URL = strings.TrimSuffix(s.cfg.sandboxURL(), "/")
	c := newChat(t, s)
	seedExchanges(t, s, c.ID, 5)
	body := `{"conversation":"` + c.ID + `","mode":"sum","keepTurns":2,"model":"m","prefix":"/anthropic/"}`

	compact(t, s, body)
	seedExchanges(t, s, c.ID, 4)
	if rec := compact(t, s, body); rec.Code != 200 {
		t.Fatalf("second compaction: status = %d, body = %s", rec.Code, rec.Body)
	}

	if len(asked) != 2 {
		t.Fatalf("summarizer called %d times, want 2", len(asked))
	}
	if !strings.Contains(asked[1], "[earlier summary]") {
		t.Error("the second request did not carry the earlier briefing; it would be lost")
	}
	live, _ := s.store.LiveTurns(c.ID)
	summaries := 0
	for _, turn := range live {
		if turn.Kind == store.TurnSummary {
			summaries++
		}
	}
	if summaries != 1 {
		t.Errorf("live summaries = %d, want 1 — the old briefing must stop being sent", summaries)
	}

	// The regression this guards: a summary's seq is the next free one at the time
	// it was written, not its position, so dropping turns by SEQ RANGE spans from
	// the old briefing's high seq forward — over the recent turns — while leaving
	// the old turns it was supposed to replace live. Addressing them by id is what
	// makes "replace the middle, keep the tail" mean that.
	//
	// After compacting 9 exchanges keeping 2, exactly one briefing plus the last
	// two exchanges may still be sent.
	if len(live) != 1+4 {
		kinds := make([]string, 0, len(live))
		for _, turn := range live {
			kinds = append(kinds, fmt.Sprintf("%s#%d", turn.Kind, turn.Seq))
		}
		t.Fatalf("live turns = %v, want a briefing plus the last 2 exchanges", kinds)
	}
	var summarySeq int
	for _, turn := range live {
		if turn.Kind == store.TurnSummary {
			summarySeq = turn.CoversTo
		}
	}
	for _, turn := range live {
		if turn.Kind != store.TurnSummary && turn.Seq <= summarySeq {
			t.Errorf("turn #%d is still live but the briefing covers up to #%d", turn.Seq, summarySeq)
		}
	}
}
