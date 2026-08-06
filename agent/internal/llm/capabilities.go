package llm

import "strings"

// Model-capability normalization for the Anthropic dialect.
//
// The agent loop asks for adaptive thinking and an effort level uniformly,
// because that is right for the current frontier models. Older models reject
// both outright — a stage pinned to Haiku 4.5 dies on the first call with
// "adaptive thinking is not supported on this model" (HTTP 400), taking the
// whole run's DAG down with it. Rather than make every call site reason about
// model families, the Anthropic client normalizes the request on the way out.
//
// Rules (per the Messages API):
//   - Claude 4.6 and later (Opus 4.6+, Sonnet 4.6+, Opus 5, Sonnet 5, Fable 5):
//     thinking:{type:"adaptive"}; output_config.effort supported.
//   - Claude 4.5 and earlier (Haiku 4.5, Sonnet 4.5, …): adaptive is rejected;
//     thinking needs the fixed-budget form. effort is rejected on Sonnet 4.5 and
//     Haiku 4.5, so it is dropped for the whole legacy tier.
//
// Unknown models are left alone: a model released after this code was written
// is far more likely to be a new frontier model than an old one, and silently
// stripping thinking would be a quiet capability regression.

// legacyThinkingModels are model-ID fragments for Claude 4.5-and-earlier, which
// reject thinking:{type:"adaptive"} and (for the Sonnet/Haiku tier) effort.
var legacyThinkingModels = []string{
	"haiku-4-5",
	"sonnet-4-5",
	"opus-4-5",
	"opus-4-1",
	"claude-3-", // claude-3-opus, claude-3-5-sonnet, claude-3-haiku, …
	"claude-2",  // claude-2.0 / claude-2.1
}

// minThinkingBudget is the API's floor for budget_tokens on the legacy form.
const minThinkingBudget = 1024

// Web-search server-tool ids. The current variant filters results with code
// execution before they reach the context window and needs Claude 4.6 or later;
// the legacy tier only knows the basic one. Same normalization argument as
// thinking: a stage pinned to an older model should search less well, not 400.
const (
	WebSearchTool       = "web_search_20260209"
	webSearchToolLegacy = "web_search_20250305"
)

// isLegacyThinking reports whether model predates adaptive thinking.
func isLegacyThinking(model string) bool {
	m := strings.ToLower(model)
	for _, frag := range legacyThinkingModels {
		if strings.Contains(m, frag) {
			return true
		}
	}
	return false
}

// normalize rewrites a request so it is valid for the target model. It returns
// the request unchanged for models that accept the modern shape.
//
// For legacy models it converts adaptive thinking to the fixed-budget form and
// drops output_config.effort. budget_tokens must be < max_tokens and at least
// minThinkingBudget; when max_tokens leaves no room, thinking is dropped rather
// than sent with an invalid budget.
func normalize(req Request) Request {
	if !isLegacyThinking(req.Model) {
		return req
	}

	// effort is rejected on this tier.
	req.OutputConfig = nil
	req.Tools = downgradeServerTools(req.Tools)

	if req.Thinking == nil || req.Thinking.Type != "adaptive" {
		return req
	}
	budget := req.MaxTokens / 2
	if budget < minThinkingBudget {
		// No room for a valid budget under max_tokens — run without thinking.
		req.Thinking = nil
		return req
	}
	req.Thinking = &Thinking{Type: "enabled", BudgetTokens: budget}
	return req
}

// downgradeServerTools rewrites server-tool ids to the variant a legacy model
// understands. The slice is copied rather than patched in place because the
// caller builds tool definitions once and reuses them across every turn.
func downgradeServerTools(in []Tool) []Tool {
	var out []Tool
	for i, t := range in {
		if t.Type != WebSearchTool {
			continue
		}
		if out == nil {
			out = make([]Tool, len(in))
			copy(out, in)
		}
		out[i].Type = webSearchToolLegacy
	}
	if out == nil {
		return in
	}
	return out
}
