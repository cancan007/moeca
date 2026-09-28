package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"orchestra/hostagent/internal/gateway"
	"orchestra/hostagent/internal/store"
)

// History compaction for a conversation.
//
// There are two layers of compaction in this product and they are different
// things. Inside a single run, the agent runtime summarizes its own tool loop
// when it approaches its context budget (agent/internal/agent/compact.go). This
// is the other layer: the transcript BETWEEN turns, which no run can see,
// because each turn is a fresh container that is handed whatever history the
// caller decided to send.
//
// What makes this layer cheap is structural rather than clever. A turn's stage
// logs and tool traces never enter the transcript at all — they live in the run
// archive and are visible in Audit — and the files a turn produced stay in the
// conversation's directory rather than being quoted into it. So the transcript
// is already just what was said. That is why most conversations never need the
// model call below, and why the deterministic path is worth offering on its own.
//
// Two rules this endpoint will not break:
//
//   - A compacted turn is marked, not deleted. The text stays readable, the
//     summary stays auditable, and the same history can be re-compressed under
//     different settings later.
//   - If the summarizer fails, NOTHING changes. Dropping turns we could not
//     summarize would silently lose the conversation to save tokens, which is
//     the opposite of the trade the operator agreed to. The agent runtime makes
//     the same choice for the same reason.
const (
	// compactTimeout is generous: reducing a long transcript is a bigger request
	// than any ticket read, and a compaction that times out leaves the caller
	// unable to distinguish a slow model from a broken gateway.
	compactTimeout = 90 * time.Second
	// clipPerTurn bounds how much of any single turn is fed to the summarizer, so
	// one enormous message cannot dominate the briefing. Mirrors maxRenderedBlock
	// in the agent runtime.
	clipPerTurn = 2000
	// summaryMaxBudget caps the briefing itself. The agent runtime uses a flat
	// 1024; a chat spanning many topics is asked to replace much more than one
	// tool loop, so the ceiling is higher and the actual ask is proportional (see
	// summaryBudget).
	summaryMaxBudget = 2048
)

// summarySystem instructs the summarizer. It replaces the earlier part of a
// conversation, so it has to be self-contained enough to keep talking from.
//
// It differs from the agent runtime's equivalent on purpose: that one condenses
// a coding session (files touched, commands run), this one condenses a dialogue,
// where what matters is what the person asked for and what was settled.
const summarySystem = "You condense the earlier part of a conversation between a person and an assistant into a compact, factual briefing, so the assistant can continue without the raw history. Preserve: what the person is trying to achieve, constraints and preferences they stated, decisions reached, conclusions and figures given, files produced (by name only — their contents are on disk), and anything still open or promised. Drop pleasantries and restatements. Write in the language the conversation is in. Output only the briefing text."

// summaryBudget is how many output tokens the briefing may use, given the size
// of what it replaces. Proportional so a short compaction is not asked for an
// essay, capped so a very long one cannot become its own context problem.
func summaryBudget(replaced int) int {
	b := replaced / 12
	if b < 256 {
		b = 256
	}
	if b > summaryMaxBudget {
		b = summaryMaxBudget
	}
	return b
}

// compactRequest is what the UI sends. The compactor's model and route arrive
// resolved, exactly as a run spec does: the frontend owns the provider and agent
// catalog, and this service deliberately does not keep a second copy of it.
type compactRequest struct {
	Conversation string `json:"conversation"`
	// Mode "recent" drops old turns with no model call at all; "sum" replaces
	// them with a briefing. Anything else is refused rather than guessed at.
	Mode string `json:"mode"`
	// KeepTurns is how many trailing EXCHANGES stay verbatim. Counted in
	// exchanges rather than messages because a chat turn is a user message and
	// the answer to it, and cutting between the two would leave a question with
	// no answer or an answer to nothing.
	KeepTurns int `json:"keepTurns"`
	// Prefix is the gateway route of the provider to summarize with, e.g.
	// "/anthropic/". Model is one of that provider's models.
	Prefix string `json:"prefix"`
	Model  string `json:"model"`
	// System overrides the built-in summarizer prompt (the compactor agent's own
	// system prompt, when one is set on it).
	System string `json:"system"`
}

// handleChatCompact reduces a conversation's live history.
func (s *Server) handleChatCompact(w http.ResponseWriter, r *http.Request) {
	if !s.storeReady(w) {
		return
	}
	var req compactRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Mode != "sum" && req.Mode != "recent" {
		writeErr(w, 400, "mode must be \"sum\" or \"recent\"")
		return
	}
	c, err := s.store.ChatByID(req.Conversation)
	if err != nil || c == nil {
		writeErr(w, 404, "unknown conversation: "+req.Conversation)
		return
	}
	live, err := s.store.LiveTurns(c.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	keep := req.KeepTurns
	if keep <= 0 {
		keep = 4
	}
	cut := cutIndex(live, keep)
	if cut <= 0 {
		// Nothing but the tail: there is no middle worth replacing, and saying so
		// is more useful than writing a summary of two messages.
		writeJSON(w, 200, map[string]any{"compacted": false, "reason": "nothing to compact"})
		return
	}
	middle := live[:cut]
	// The range the briefing will stand for is measured over real turns only. An
	// earlier summary sitting in the middle carries the seq it was WRITTEN at,
	// which is above everything, so including it would put the new briefing's
	// covers_to past the turns that stay verbatim — and covers_to is what decides
	// where a summary reads.
	ids := make([]int64, 0, len(middle))
	from, to := 0, 0
	replaced := 0
	for _, t := range middle {
		ids = append(ids, t.ID)
		replaced += t.TokensEst
		if t.Kind == store.TurnSummary {
			continue
		}
		if from == 0 || t.Seq < from {
			from = t.Seq
		}
		if t.Seq > to {
			to = t.Seq
		}
	}
	if to == 0 {
		// Nothing but an old briefing: re-summarizing one summary buys nothing.
		writeJSON(w, 200, map[string]any{"compacted": false, "reason": "nothing to compact"})
		return
	}

	summary := ""
	if req.Mode == "sum" {
		gw := gateway.NewWithTimeout(s.cfg.Gateway.URL, s.cfg.Gateway.Session, compactTimeout)
		if !gw.Configured() {
			writeErr(w, 503, "no gateway configured: cannot summarize (history left unchanged)")
			return
		}
		summary, err = summarizeTurns(r.Context(), gw, req, middle, replaced)
		if err != nil {
			// Best-effort in the agent runtime's sense: the failure is reported and
			// the transcript is left exactly as it was.
			writeErr(w, 502, "summarizing failed, history left unchanged: "+err.Error())
			return
		}
	}

	if summary != "" {
		// The summary sits at the position of the range it replaces, so the
		// conversation still reads in order. It takes the seq of its last covered
		// turn plus a fraction of the gap — there is none, so it goes after the
		// range and before the tail by taking `to`'s place in ordering terms: a
		// new seq at the end would put the briefing after the messages it is
		// supposed to precede.
		if err := s.store.AddSummaryTurn(c.ID, summary, from, to, estimateTokens(summary)); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	if err := s.store.MarkDropped(ids); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	_ = s.store.TouchChat(c.ID)

	turns, err := s.store.Turns(c.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"compacted": true,
		"mode":      req.Mode,
		// What the operator actually wants to know: how much of the context this
		// bought back. Reported rather than estimated in the UI so the number
		// shown is the one that happened.
		"replacedTokens": replaced,
		"summaryTokens":  estimateTokens(summary),
		"coversFrom":     from,
		"coversTo":       to,
		"turns":          turns,
	})
}

// cutIndex returns how many of the leading turns may be replaced, keeping the
// last `keep` exchanges verbatim and never splitting a user message from the
// answer to it.
//
// Walking backwards over user turns is what counts exchanges: a summary turn in
// the tail belongs to whatever is around it, and an agent turn without its
// question is not an exchange. The cut lands ON the oldest question being kept,
// so the answer to the question before it goes with that question rather than
// being stranded in the tail.
func cutIndex(live []store.ChatTurn, keep int) int {
	seen := 0
	for i := len(live) - 1; i >= 0; i-- {
		if live[i].Kind != store.TurnUser {
			continue
		}
		seen++
		if seen == keep {
			return i // the tail begins here, on a question
		}
	}
	return 0 // fewer exchanges than we were asked to keep: nothing to compact
}

// renderTurns flattens turns into the plain text handed to the summarizer, each
// clipped so one huge message cannot crowd out the rest.
func renderTurns(turns []store.ChatTurn) string {
	var b strings.Builder
	for _, t := range turns {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		if len([]rune(text)) > clipPerTurn {
			text = string([]rune(text)[:clipPerTurn]) + "…"
		}
		switch t.Kind {
		case store.TurnUser:
			b.WriteString("person: ")
		case store.TurnSummary:
			b.WriteString("[earlier summary]: ")
		default:
			b.WriteString("assistant: ")
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return b.String()
}

// summarizeTurns asks the model, through the gateway, for the briefing.
//
// The request carries no tools and no thinking: this is a text reduction, and
// granting a summarizer anything else would be granting it to a call the operator
// never sees. Only the Anthropic dialect is spoken here — the UI restricts the
// choice of compactor accordingly, rather than this service pretending to
// support a shape it would get subtly wrong.
func summarizeTurns(ctx context.Context, gw *gateway.Client, req compactRequest, middle []store.ChatTurn, replaced int) (string, error) {
	system := strings.TrimSpace(req.System)
	if system == "" {
		system = summarySystem
	}
	body, err := json.Marshal(map[string]any{
		"model":      req.Model,
		"max_tokens": summaryBudget(replaced),
		"system":     system,
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "text",
				"text": "Conversation to condense:\n\n" + renderTurns(middle),
			}},
		}},
	})
	if err != nil {
		return "", err
	}
	prefix := req.Prefix
	if prefix == "" {
		prefix = "/anthropic/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	raw, err := gw.Do(ctx, "POST", prefix+"v1/messages", body)
	if err != nil {
		return "", err
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, blk := range resp.Content {
		if blk.Type == "text" {
			out.WriteString(blk.Text)
		}
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", fmt.Errorf("the summarizer returned no text")
	}
	return text, nil
}
