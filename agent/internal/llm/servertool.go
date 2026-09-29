package llm

import (
	"encoding/json"
	"strings"
)

// A server tool's work can straddle a turn.
//
// The provider runs some tools itself — the current web search filters its
// results with code execution — and the call does not always finish inside the
// response that started it: a `server_tool_use` block can arrive with its
// result coming at the top of the NEXT response. That is normal, and the
// conversation continues through it.
//
// What is not allowed is answering such a turn with anything other than tool
// results. A run died on exactly that: the model asked to look at three images
// in the same turn the provider had code still running, the images rode back in
// the same user message as the results, and the provider refused the whole
// request:
//
//	`bash_code_execution` tool use with id `srvtoolu_...` was found without a
//	corresponding `bash_code_execution_tool_result` block
//
// The images were the problem, not the code execution. Two identical turns
// before it — carrying tool results and nothing else — went through fine.

// PendingServerToolUse reports whether an assistant turn contains a server tool
// call whose result has not arrived yet, meaning the reply to it must carry
// tool results and nothing besides.
func PendingServerToolUse(content []Block) bool {
	var called []string
	answered := map[string]bool{}
	for _, b := range content {
		switch {
		case b.Type == "server_tool_use":
			if id := rawField(b.Raw, "id"); id != "" {
				called = append(called, id)
			}
		case strings.HasSuffix(b.Type, "_tool_result"):
			// Every server-tool result names the call it belongs to, whatever
			// the tool: bash_code_execution_tool_result, web_search_tool_result
			// and the rest all carry tool_use_id.
			if id := rawField(b.Raw, "tool_use_id"); id != "" {
				answered[id] = true
			}
		}
	}
	for _, id := range called {
		if !answered[id] {
			return true
		}
	}
	return false
}

// WebSearchCallBlock is how the OpenAI Responses dialect reports a search it
// performed. Anthropic reports the same event as a server_tool_use block named
// web_search; both arrive as unmodelled blocks kept verbatim, which is what lets
// one counter serve both.
const WebSearchCallBlock = "web_search_call"

// WebSearchUse is one provider-side search, as far as this side can see it.
type WebSearchUse struct {
	// Action is what the provider did: a plain search, or — in the OpenAI
	// dialect, whose one grant covers more than searching — opening or reading a
	// page. Empty means the dialect does not say.
	Action string
	// Query is the search terms, when the provider reports them.
	Query string
}

// WebSearchUses lists the provider-side searches in one response.
//
// It exists because the provider's own cap is not something every dialect can be
// told. Anthropic takes `max_uses` and enforces it; the OpenAI Responses API has
// no equivalent field, so the agent has to count what came back and stop
// offering the tool once the grant is spent. Counting is also the only way the
// run log can say how many searches a turn actually made — which is worth having
// even where the provider does enforce the cap, because a bill arrives either
// way.
func WebSearchUses(content []Block) []WebSearchUse {
	var out []WebSearchUse
	for _, b := range content {
		switch {
		case b.Type == BlockServerToolUse && b.ServerToolName() == "web_search":
			out = append(out, WebSearchUse{Action: "search", Query: rawQuery(b.Raw)})
		case b.Type == WebSearchCallBlock:
			out = append(out, WebSearchUse{Action: webSearchAction(b.Raw), Query: rawQuery(b.Raw)})
		}
	}
	return out
}

// webSearchAction reads action.type from an OpenAI web_search_call item:
// "search", "open_page" or "find_in_page". Each is a billable action, so each
// counts.
func webSearchAction(raw []byte) string {
	var t struct {
		Action struct {
			Type string `json:"type"`
		} `json:"action"`
	}
	if json.Unmarshal(raw, &t) != nil || t.Action.Type == "" {
		return "search"
	}
	return t.Action.Type
}

// rawQuery pulls the search terms out of whichever shape carries them:
// Anthropic puts them in `input.query`, the Responses API in `action.query`.
func rawQuery(raw []byte) string {
	var t struct {
		Input struct {
			Query string `json:"query"`
		} `json:"input"`
		Action struct {
			Query string `json:"query"`
			URL   string `json:"url"`
		} `json:"action"`
	}
	if json.Unmarshal(raw, &t) != nil {
		return ""
	}
	switch {
	case t.Input.Query != "":
		return t.Input.Query
	case t.Action.Query != "":
		return t.Action.Query
	default:
		// open_page / find_in_page name a URL rather than a query, and which page
		// the model opened is the more interesting fact of the two.
		return t.Action.URL
	}
}

// rawField pulls one string field out of a block kept verbatim.
func rawField(raw []byte, name string) string {
	if len(raw) == 0 {
		return ""
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(probe[name], &s); err != nil {
		return ""
	}
	return s
}
