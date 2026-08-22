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
