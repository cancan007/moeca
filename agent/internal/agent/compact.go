package agent

import (
	"context"
	"strings"

	"orchestra/agent/internal/llm"
)

// DefaultKeepRecent is how many trailing turns survive a compaction verbatim.
const DefaultKeepRecent = 6

// summarySystem instructs the summarizer model. The briefing replaces the raw
// middle of the transcript, so it must be self-contained enough to continue on.
const summarySystem = "You condense the earlier part of a coding agent's session into a compact, factual briefing so the agent can continue without the raw history. Preserve: the goal, decisions made, files created or edited and their current state, commands run and their results, and any unresolved problems or next steps. Be concise and specific. Output only the briefing text."

// maxRenderedBlock caps how much of any single block is fed to the summarizer,
// so one huge tool result cannot dominate the briefing request.
const maxRenderedBlock = 2000

// maybeCompact summarizes the middle of the conversation when the context has
// grown past cfg.MaxContextTokens (using the previous turn's real input-token
// count, or a char-based estimate when the provider reports none). It preserves
// the original task and the most recent turns, and rewrites everything between
// them into a single summary turn. Compaction is best-effort: on any summarizer
// error the original history is returned unchanged so the run never fails here.
func (r *Runner) maybeCompact(ctx context.Context, msgs []llm.Message, lastInputTokens int) ([]llm.Message, bool) {
	if r.cfg.MaxContextTokens <= 0 {
		return msgs, false
	}
	metric := lastInputTokens
	if metric <= 0 {
		metric = estimateTokens(msgs)
	}
	if metric < r.cfg.MaxContextTokens {
		return msgs, false
	}

	cut := safeCut(msgs, r.cfg.KeepRecent)
	if cut <= 1 {
		return msgs, false // not enough middle to be worth summarizing
	}
	middle := msgs[1:cut]

	summary, err := r.summarize(ctx, middle)
	if err != nil || strings.TrimSpace(summary) == "" {
		if err != nil {
			r.log.event(logLine{Type: "compaction", Message: "summarize failed: " + err.Error()})
		}
		return msgs, false
	}

	// Fold the original task and the summary into one user turn. The retained
	// tail begins on an assistant turn (safeCut guarantees it), so roles still
	// alternate and no tool_result is left without its tool_use.
	head := llm.Message{Role: "user", Content: []llm.Block{
		llm.TextBlock(r.cfg.Task + "\n\n---\n[Summary of the session so far]\n" + summary),
	}}
	out := append([]llm.Message{head}, msgs[cut:]...)

	r.log.event(logLine{
		Type:    "compaction",
		Message: "history summarized",
		Before:  len(msgs),
		After:   len(out),
		Tokens:  metric,
	})
	return out, true
}

// summarize asks the provider to condense a slice of turns into a briefing.
// The request carries no tools and no thinking — it is a plain text reduction.
func (r *Runner) summarize(ctx context.Context, middle []llm.Message) (string, error) {
	req := llm.Request{
		Model:     r.cfg.Model,
		MaxTokens: 1024,
		System:    summarySystem,
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.Block{llm.TextBlock("Session transcript to summarize:\n\n" + renderTranscript(middle))},
		}},
	}
	resp, err := r.cfg.Provider.CreateMessage(ctx, req)
	if err != nil {
		return "", err
	}
	return textOf(resp.Content), nil
}

// safeCut returns the index at which the retained tail should begin: the latest
// assistant turn at or before len-keep, so the tail keeps at least `keep` turns
// and starts on an assistant turn. Returns 0 when there is no viable boundary.
func safeCut(msgs []llm.Message, keep int) int {
	if keep <= 0 {
		keep = DefaultKeepRecent
	}
	target := len(msgs) - keep
	if target <= 1 {
		return 0
	}
	for c := target; c >= 1; c-- {
		if msgs[c].Role == "assistant" {
			return c
		}
	}
	return 0
}

// renderTranscript flattens turns into plain text for the summarizer: text
// blocks verbatim, tool calls and results as compact one-liners, thinking (Raw)
// blocks dropped. Each block is truncated to maxRenderedBlock characters.
func renderTranscript(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		for _, blk := range m.Content {
			switch blk.Type {
			case llm.BlockText:
				if blk.Text == "" {
					continue
				}
				b.WriteString(m.Role)
				b.WriteString(": ")
				b.WriteString(clip(blk.Text))
				b.WriteByte('\n')
			case llm.BlockToolUse:
				b.WriteString("→ tool ")
				b.WriteString(blk.Name)
				b.WriteString("(")
				b.WriteString(clip(string(blk.Input)))
				b.WriteString(")\n")
			case llm.BlockToolResult:
				b.WriteString("← result")
				if blk.IsError {
					b.WriteString(" [error]")
				}
				b.WriteString(": ")
				b.WriteString(clip(blk.Content))
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// textOf concatenates the text blocks of a content slice.
func textOf(content []llm.Block) string {
	var b strings.Builder
	for _, blk := range content {
		if blk.Type == llm.BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// estimateTokens is a rough char/4 estimate over all message content, used when
// the provider does not report input_tokens.
func estimateTokens(msgs []llm.Message) int {
	chars := 0
	for _, m := range msgs {
		for _, blk := range m.Content {
			chars += len(blk.Text) + len(blk.Content) + len(blk.Input) + len(blk.Raw)
		}
	}
	return chars / 4
}

func clip(s string) string {
	if len(s) > maxRenderedBlock {
		return s[:maxRenderedBlock] + "…"
	}
	return s
}
