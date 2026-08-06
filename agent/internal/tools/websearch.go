// Web search — the one tool in this package the agent does not run.
//
// Everything else executes inside the container: the file tools touch /work,
// the media and HTTP tools POST through the gateway. Web search is different.
// It is a provider-side SERVER tool: the agent advertises it in the same
// `tools` array, Anthropic performs the search on its own infrastructure, and
// the query and its results come back as extra content blocks in the same
// response. The container never opens a socket to the web, the sandbox's
// egress island is unchanged, and the gateway sees exactly the /v1/messages
// call it already sees — no new upstream, no new key, no new allowlist entry.
//
// That is why this is preferred over wiring a search API up as a custom HTTP
// tool. The cost is that it only exists in the Anthropic dialect: the OpenAI
// and Gemini encoders drop server tools, and main.go does not grant this one to
// those providers at all, so nobody is handed a tool that silently does nothing.
//
// Otherwise it follows the media pattern: absent configuration means an absent
// tool, so an agent that was not granted search cannot ask for it. Searches are
// billed per use on top of tokens, which is why MaxUses is capped by default
// rather than left open.
package tools

import "orchestra/agent/internal/llm"

const (
	// WebSearchToolName is the name the model calls; Anthropic executes it.
	WebSearchToolName = "web_search"
	// defaultWebSearchMaxUses bounds searches per run when the grant does not.
	// A loop that can search without limit is a loop that can spend without
	// limit, and this spend is per search rather than per token.
	defaultWebSearchMaxUses = 5
)

// WebSearchConfig is the ORCHESTRA_WEB_SEARCH payload: the shape of the grant,
// not of a provider route (there is no route — the provider searches for us).
type WebSearchConfig struct {
	// MaxUses caps searches in one run. 0 => defaultWebSearchMaxUses.
	MaxUses int `json:"maxUses,omitempty"`
	// AllowedDomains restricts search to these domains; BlockedDomains excludes
	// them. The API rejects both at once, so Allowed wins when both are set —
	// it is the narrower of the two statements.
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	BlockedDomains []string `json:"blockedDomains,omitempty"`
}

// SetWebSearch grants the web_search server tool to this agent.
func (r *Registry) SetWebSearch(cfg WebSearchConfig) { r.webSearch = &cfg }

// webSearchDefinition returns the server-tool definition, or nil when search
// was not granted.
func (r *Registry) webSearchDefinition() []llm.Tool {
	if r.webSearch == nil {
		return nil
	}
	maxUses := r.webSearch.MaxUses
	if maxUses <= 0 {
		maxUses = defaultWebSearchMaxUses
	}
	def := llm.Tool{
		Name:    WebSearchToolName,
		Type:    llm.WebSearchTool,
		MaxUses: maxUses,
	}
	// Mutually exclusive on the wire: sending both is a 400, so the narrower
	// grant is honoured and the other dropped rather than failing the run.
	if len(r.webSearch.AllowedDomains) > 0 {
		def.AllowedDomains = r.webSearch.AllowedDomains
	} else if len(r.webSearch.BlockedDomains) > 0 {
		def.BlockedDomains = r.webSearch.BlockedDomains
	}
	return []llm.Tool{def}
}

// webSearchMisdispatch is returned if the model ever emits a client-style
// tool_use for web_search. It cannot happen through the documented flow, but
// answering "unknown tool" would be actively misleading — the tool exists, it
// is just not ours to run.
const webSearchMisdispatch = "web_search is executed by the model provider, not by this agent; " +
	"its results are returned automatically in the same response — do not call it as a client tool"
