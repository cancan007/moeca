// Package agent runs the tool-use loop: it drives Claude's Messages API turn by
// turn, executes the tool calls Claude requests against the /work worktree, and
// feeds the results back until Claude finishes (stop_reason "end_turn").
//
// Every turn is reported to stdout as one A2A-style JSON log line (captured by
// `docker logs`), carrying the role, stop reason, the tool calls issued, and
// the token usage from response.usage.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"orchestra/agent/internal/llm"
	"orchestra/agent/internal/tools"
)

// DefaultMaxTokens is the per-response output cap.
const DefaultMaxTokens = 16000

// DefaultMaxIterations bounds the tool-use loop to avoid runaway conversations.
const DefaultMaxIterations = 40

// Config parameterises one agent run.
type Config struct {
	Model     string
	System    string
	Task      string
	MaxTokens int
	MaxIter   int
	// Effort tunes thinking depth and token spend (low|medium|high|xhigh|max).
	// Empty leaves it unset (the API default, "high"). Lowering it is the primary
	// per-run cost lever; the sandbox sources it from ORCHESTRA_EFFORT.
	Effort   string
	Provider llm.Provider
	Tools    *tools.Registry
	LogW     io.Writer        // where A2A log lines go (default os.Stdout via New)
	Now      func() time.Time // injectable clock (tests)

	// MaxContextTokens triggers history compaction: when the previous turn's
	// input-token count (or an estimate) exceeds it, the middle of the
	// conversation is summarized. 0 disables compaction.
	MaxContextTokens int
	// KeepRecent is how many trailing turns survive a compaction verbatim
	// (default DefaultKeepRecent).
	KeepRecent int
}

// Runner executes the tool-use loop for a Config.
type Runner struct {
	cfg Config
	log *logger
}

// NewRunner builds a Runner, filling zero-valued defaults.
func NewRunner(cfg Config) *Runner {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.MaxIter <= 0 {
		cfg.MaxIter = DefaultMaxIterations
	}
	if cfg.KeepRecent <= 0 {
		cfg.KeepRecent = DefaultKeepRecent
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Runner{cfg: cfg, log: newLogger(cfg.LogW, cfg.Now)}
}

// Run drives the loop to completion. It returns nil when Claude ends its turn,
// and an error on a fatal API failure or when the iteration cap is hit.
func (r *Runner) Run(ctx context.Context) error {
	r.log.event(logLine{Type: "task_start", Model: r.cfg.Model, Task: r.cfg.Task})

	messages := []llm.Message{
		{Role: "user", Content: []llm.Block{llm.TextBlock(r.cfg.Task)}},
	}
	toolDefs := r.cfg.Tools.Definitions()

	// lastInput carries the previous turn's real input-token count so compaction
	// can trigger on measured context growth rather than a guess.
	lastInput := 0
	for i := 0; i < r.cfg.MaxIter; i++ {
		if compacted, did := r.maybeCompact(ctx, messages, lastInput); did {
			messages = compacted
			lastInput = 0 // the next response reports the post-compaction size
		}

		req := llm.Request{
			Model:     r.cfg.Model,
			MaxTokens: r.cfg.MaxTokens,
			System:    r.cfg.System,
			Thinking:  &llm.Thinking{Type: "adaptive"},
			Messages:  messages,
			Tools:     toolDefs,
		}
		if r.cfg.Effort != "" {
			req.OutputConfig = &llm.OutputConfig{Effort: r.cfg.Effort}
		}

		resp, err := r.cfg.Provider.CreateMessage(ctx, req)
		if err != nil {
			r.log.event(logLine{Type: "error", Iteration: i, Message: err.Error()})
			return fmt.Errorf("agent: iteration %d: %w", i, err)
		}
		lastInput = resp.Usage.InputTokens

		toolCalls := collectToolNames(resp.Content)
		r.log.event(logLine{
			Type:       "turn",
			Iteration:  i,
			Role:       resp.Role,
			StopReason: resp.StopReason,
			ToolCalls:  toolCalls,
			Usage:      &resp.Usage,
		})

		switch resp.StopReason {
		case "end_turn":
			r.log.event(logLine{Type: "task_done", Iteration: i, StopReason: resp.StopReason})
			return nil
		case "max_tokens":
			r.log.event(logLine{Type: "task_stopped", Iteration: i, StopReason: resp.StopReason,
				Message: "response hit max_tokens; stopping"})
			return nil
		case "tool_use":
			// Echo the full assistant content back verbatim (thinking + tool_use).
			messages = append(messages, llm.Message{Role: "assistant", Content: resp.Content})
			results := r.executeTools(resp.Content)
			messages = append(messages, llm.Message{Role: "user", Content: results})
		case "pause_turn":
			// A server tool (web search) hit the provider's per-turn iteration
			// cap. The turn is unfinished, not over, and nothing ran on this
			// side — so echo it back verbatim with no tool_result and the
			// provider resumes where it stopped. MaxIter still bounds this.
			messages = append(messages, llm.Message{Role: "assistant", Content: resp.Content})
		default:
			r.log.event(logLine{Type: "task_stopped", Iteration: i, StopReason: resp.StopReason,
				Message: "unexpected stop_reason; stopping"})
			return nil
		}
	}

	r.log.event(logLine{Type: "error", Message: "reached max iterations without end_turn"})
	return fmt.Errorf("agent: reached max iterations (%d) without end_turn", r.cfg.MaxIter)
}

// executeTools runs every tool_use block in the assistant content and returns
// the matching tool_result blocks (one per tool_use, same tool_use_id).
func (r *Runner) executeTools(content []llm.Block) []llm.Block {
	var results []llm.Block
	for _, b := range content {
		if b.Type != llm.BlockToolUse {
			continue
		}
		var args map[string]any
		if len(b.Input) > 0 {
			if err := json.Unmarshal(b.Input, &args); err != nil {
				r.log.event(logLine{Type: "tool_result", Tool: b.Name, IsError: true,
					Message: "bad tool input JSON: " + err.Error()})
				results = append(results, llm.ToolResultBlock(b.ID, "invalid tool input: "+err.Error(), true))
				continue
			}
		}
		out, isErr := r.cfg.Tools.Dispatch(b.Name, args)
		r.log.event(logLine{Type: "tool_result", Tool: b.Name, IsError: isErr})
		results = append(results, llm.ToolResultBlock(b.ID, out, isErr))
	}
	return results
}

// collectToolNames extracts the tool names of a turn for logging. Both kinds
// count: a provider-executed server tool (web_search) never reaches
// executeTools, so without this a turn that searched three times would log as
// a turn that used no tools at all.
func collectToolNames(content []llm.Block) []string {
	var names []string
	for _, b := range content {
		switch {
		case b.Type == llm.BlockToolUse:
			names = append(names, b.Name)
		case b.Type == llm.BlockServerToolUse:
			if n := b.ServerToolName(); n != "" {
				names = append(names, n)
			}
		}
	}
	return names
}
