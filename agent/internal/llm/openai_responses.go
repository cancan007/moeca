package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// openAIResponsesClient speaks the OpenAI Responses API (POST /v1/responses)
// through the gateway.
//
// This is a second OpenAI dialect rather than a flag on the first one because
// the two endpoints disagree about more than a path: Chat Completions takes a
// flat list of messages and answers with one message, while Responses takes and
// returns a list of typed ITEMS — message, function_call, reasoning — and the
// translation to/from the neutral Request/Response is a different function, not
// a different constant.
//
// It exists because the reasoning-first models require it. gpt-6-astra refuses
// to combine function tools with reasoning on /v1/chat/completions:
//
//	400 Function tools with reasoning_effort are not supported for gpt-6-astra
//	in /v1/chat/completions. To use function tools, use /v1/responses or set
//	reasoning_effort to 'none'.
//
// The second half of that advice is not available here. Astra rejects effort
// "none" outright, and a coding agent without tools is not an agent — so for a
// model of this family the endpoint is not a preference, it is the only way to
// use the model at all.
type openAIResponsesClient struct {
	baseURL string
	gctx    GatewayCtx
	http    *http.Client
}

// Reasoning state stays on this side, encrypted.
//
// A reasoning model's thinking is state: without it the model re-derives its
// plan after every tool call. The Responses API offers two ways to keep it
// across a turn — let OpenAI hold the conversation (store:true, then name it
// with previous_response_id), or carry an ENCRYPTED copy of the reasoning in
// each request (store:false plus the reasoning.encrypted_content include, which
// OpenAI decrypts in memory, uses, and discards). This client always takes the
// second.
//
// That is a policy choice, and taking it silently is only defensible because it
// is the conservative one: it is also what a zero-data-retention organization
// must do, so it works everywhere. Storing the conversation provider-side is a
// legitimate trade — cost and latency against data residency — but it is an
// operator's trade to make, not a default to inherit. When it becomes a
// per-provider setting, this is the constant it replaces.
const (
	orStore          = false
	orIncludeRsnEncr = "reasoning.encrypted_content"
	// Without this the web_search_call items come back without their `results`
	// array, and the pictures a search found are invisible from here — which is
	// exactly what happened: twenty-one searches, every one of them reported as
	// returning no images, because nothing had asked for the array that says.
	// Asking for it is what makes the count mean anything.
	orIncludeSearchResults = "web_search_call.results"
)

/* ─── request ─── */

type orRequest struct {
	Model string `json:"model"`
	// Instructions is this dialect's system prompt.
	Instructions string `json:"instructions,omitempty"`
	// Input is a heterogeneous item list: modelled structs for what this
	// package understands, raw bytes for what it only carries (see encode).
	Input           []any        `json:"input"`
	Tools           []orTool     `json:"tools,omitempty"`
	MaxOutputTokens int          `json:"max_output_tokens,omitempty"`
	Reasoning       *orReasoning `json:"reasoning,omitempty"`
	Include         []string     `json:"include,omitempty"`
	// Store has no omitempty on purpose: false is the value we mean to send,
	// not a field we forgot. Omitting it would opt into the API default, which
	// is to keep the conversation.
	Store bool `json:"store"`
}

type orReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// orTool is a custom function. Note the shape is flat — name/description/
// parameters sit on the tool itself, where Chat Completions nests them under a
// "function" object.
type orTool struct {
	Type string `json:"type"`
	// A built-in tool is named by its type alone; only a function carries these.
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Filters is web_search's domain restriction. Unlike the Anthropic dialect,
	// which takes allowed_domains and blocked_domains as separate mutually
	// exclusive fields, this API nests both under one object.
	Filters *orWebFilters `json:"filters,omitempty"`
	// SearchContentTypes opts the search into returning pictures as well as
	// prose, and ImageSettings bounds how many come back.
	SearchContentTypes []string         `json:"search_content_types,omitempty"`
	ImageSettings      *orImageSettings `json:"image_settings,omitempty"`
}

type orWebFilters struct {
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

// orImageSettings bounds the picture half of a search. MaxResults has no default
// on the API's side — a positive number has to be asked for — and it is worth
// asking for a small one: every image the model looks at is paid for in tokens,
// and a search that quietly returned twenty of them would cost more than the
// answer is worth.
type orImageSettings struct {
	MaxResults int  `json:"max_results"`
	Caption    bool `json:"caption"`
}

// defaultImageResults is how many pictures one search may return.
//
// Three is the number the API's own example uses, and it is enough to look at a
// diagram someone linked without turning a question into a gallery. It is a
// constant rather than a setting because the grant already says whether this
// agent may search at all; how many pictures a search brings back is a cost
// detail, not a permission.
const defaultImageResults = 6

// domainFilters translates the grant's domain restriction, or nil when it names
// none. The grant already guarantees at most one of the two is set — sending
// both is a 400 upstream — so this only has to carry whichever it is.
func domainFilters(t Tool) *orWebFilters {
	if len(t.AllowedDomains) == 0 && len(t.BlockedDomains) == 0 {
		return nil
	}
	return &orWebFilters{AllowedDomains: t.AllowedDomains, BlockedDomains: t.BlockedDomains}
}

// orMessage's Content is `any` for the same reason as the Chat Completions
// dialect: a plain string covers every turn, and the array-of-parts form is
// needed only once an image is involved.
type orMessage struct {
	Type    string `json:"type,omitempty"`
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// orFunctionCall replays a tool call the model made. call_id (not id) is what
// correlates a call with its output.
type orFunctionCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON *string*
}

// orFunctionCallOutput answers a call. It is a top-level item, not a message
// with a "tool" role — this dialect has no such role.
type orFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// orImagePart is this dialect's image: a data: URI, like Chat Completions, but
// the part type is input_image and the URI is not wrapped in an object.
func orImagePart(mediaType, data string) map[string]string {
	return map[string]string{"type": "input_image", "image_url": "data:" + mediaType + ";base64," + data}
}

func orTextPart(text string) map[string]string {
	return map[string]string{"type": "input_text", "text": text}
}

// encode translates the neutral request into a Responses request.
func (c *openAIResponsesClient) encode(req Request) orRequest {
	input := make([]any, 0, len(req.Messages)+1)

	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			// An assistant turn goes back as the ITEMS the model produced, in
			// the order it produced them. Order is load-bearing: a reasoning
			// item is bound to the call it justifies, and the API rejects one
			// that arrives without the item it belongs to. That is also why
			// decode keeps the raw bytes of everything this package does not
			// model instead of flattening the turn into text — a summarized
			// assistant turn would lose the thinking the next turn needs.
			for _, b := range m.Content {
				switch {
				case b.Type == BlockToolUse:
					input = append(input, orFunctionCall{
						Type: "function_call", CallID: b.ID, Name: b.Name,
						Arguments: argsOrEmptyObject(b.Input),
					})
				case b.Type == BlockText:
					if b.Text != "" {
						input = append(input, orMessage{Role: "assistant", Content: b.Text})
					}
				case len(b.Raw) > 0:
					// reasoning (with its encrypted_content), and any item type
					// added after this code was written.
					input = append(input, json.RawMessage(b.Raw))
				}
			}
		default: // user
			var text []string
			var images []any
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					text = append(text, b.Text)
				case BlockToolResult:
					// Emitted as encountered, so every output precedes the user
					// message that may follow it in the same turn.
					input = append(input, orFunctionCallOutput{
						Type: "function_call_output", CallID: b.ToolUseID, Output: b.Content,
					})
				case BlockImage:
					images = append(images, orImagePart(b.MediaType, b.Data))
				}
			}
			switch {
			case len(images) > 0:
				parts := make([]any, 0, len(images)+1)
				if len(text) > 0 {
					parts = append(parts, orTextPart(strings.Join(text, "\n")))
				}
				parts = append(parts, images...)
				input = append(input, orMessage{Role: "user", Content: parts})
			case len(text) > 0:
				input = append(input, orMessage{Role: "user", Content: strings.Join(text, "\n")})
			}
		}
	}

	var tools []orTool
	searching := false
	for _, t := range req.Tools {
		// Provider-executed tools are translated where this API has the same
		// tool. Web search is the one that does.
		//
		// Two of the grant's three parts cross cleanly: allowed and blocked
		// domains become `filters`. The third — max_uses — has no field here at
		// all, and forwarding the tool without it would turn a bounded grant into
		// an unbounded one on a tool billed per use. So the cap is enforced by
		// the agent instead: it counts the web_search_call items that come back
		// and stops offering the tool once the grant is spent (see the loop).
		// That is a turn later than a server-side cap would stop, and the run log
		// says how many searches actually happened, so the difference is visible
		// rather than assumed.
		if t.IsServer() {
			if t.Type == WebSearchTool || t.Type == WebSearchToolLegacy {
				// Pictures are asked for explicitly. Without this the search
				// returns prose only, which is why an agent sent to look at a
				// diagram could read every word around it and still not have seen
				// it — the page reader opens a .png and finds no text in it.
				searching = true
				tools = append(tools, orTool{
					Type:               "web_search",
					Filters:            domainFilters(t),
					SearchContentTypes: []string{"text", "image"},
					ImageSettings:      &orImageSettings{MaxResults: defaultImageResults, Caption: true},
				})
			}
			continue
		}
		tools = append(tools, orTool{
			Type: "function", Name: t.Name, Description: t.Description, Parameters: t.InputSchema,
		})
	}

	// Reasoning knobs go only to models known to take them, for the reason
	// spelled out on acceptsEffort: a chat model answers an unexpected
	// reasoning argument with a 400 rather than ignoring it.
	//
	// Unlike Chat Completions' reasoning_effort, this endpoint's effort accepts
	// xhigh and max — the top two of the agent's own levels — so the value
	// passes through unclamped instead of through oaEffort.
	var reasoning *orReasoning
	var include []string
	if acceptsEffort(req.Model) {
		include = []string{orIncludeRsnEncr}
		if req.OutputConfig != nil {
			if e := strings.TrimSpace(req.OutputConfig.Effort); e != "" {
				reasoning = &orReasoning{Effort: e}
			}
		}
	}
	// Ask for what each search actually found, but only when a search was
	// granted: an include naming a tool the request does not carry is a field
	// asking about something that cannot happen.
	if searching {
		include = append(include, orIncludeSearchResults)
	}

	return orRequest{
		Model:           req.Model,
		Instructions:    req.System,
		Input:           input,
		Tools:           tools,
		MaxOutputTokens: req.MaxTokens,
		Reasoning:       reasoning,
		Include:         include,
		Store:           orStore,
	}
}

// argsOrEmptyObject keeps a tool call's arguments a valid JSON object string.
// A model that calls a no-argument tool may send nothing at all, and "" is not
// parseable JSON on the way back in.
func argsOrEmptyObject(input json.RawMessage) string {
	args := strings.TrimSpace(string(input))
	if args == "" {
		return "{}"
	}
	return args
}

/* ─── response ─── */

type orResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	// IncompleteDetails says why a response stopped short; reason
	// "max_output_tokens" is this dialect's truncation signal.
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []json.RawMessage `json:"output"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	// Error is set on a failed response, which can arrive with HTTP 200.
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// orOutputText is one part of a message item's content.
type orOutputText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// decodeItem turns one output item into a neutral block.
//
// Items this package models become typed blocks; everything else is kept
// verbatim so encode can hand it back untouched. Reasoning is the case that
// matters — its encrypted_content is the model's thinking, and it has to
// survive a round trip through the agent loop byte for byte.
func decodeItem(raw json.RawMessage) (Block, bool) {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return Block{}, false
	}
	switch probe.Type {
	case "message":
		var m struct {
			Content []orOutputText `json:"content"`
		}
		if json.Unmarshal(raw, &m) != nil {
			return Block{}, false
		}
		var parts []string
		for _, p := range m.Content {
			if p.Type == "output_text" && p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		if len(parts) == 0 {
			return Block{}, false
		}
		return TextBlock(strings.Join(parts, "\n")), true
	case "function_call":
		var f struct {
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(raw, &f) != nil {
			return Block{}, false
		}
		return Block{
			Type: BlockToolUse, ID: f.CallID, Name: f.Name,
			Input: json.RawMessage(argsOrEmptyObject(json.RawMessage(f.Arguments))),
		}, true
	default:
		return Block{Type: probe.Type, Raw: append(json.RawMessage(nil), raw...)}, true
	}
}

func (c *openAIResponsesClient) CreateMessage(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(c.encode(req))
	if err != nil {
		return nil, fmt.Errorf("openai-responses: marshal request: %w", err)
	}
	raw, status, err := httpPostJSON(ctx, c.http, c.baseURL+"/v1/responses", c.gctx, body)
	if err != nil {
		return nil, fmt.Errorf("openai-responses: post: %w", err)
	}
	if status != http.StatusOK {
		var e orResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != nil && e.Error.Message != "" {
			return nil, fmt.Errorf("openai-responses: %d (%s): %s", status, e.Error.Type, e.Error.Message)
		}
		return nil, fmt.Errorf("openai-responses: %d: %s", status, strings.TrimSpace(string(raw)))
	}

	var out orResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("openai-responses: decode response: %w", err)
	}
	// A response can fail with HTTP 200; reporting it as an error rather than an
	// empty turn keeps the failure where the stage log can show it.
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("openai-responses: %s: %s", out.Error.Type, out.Error.Message)
	}

	var content []Block
	toolUse := false
	for _, item := range out.Output {
		b, ok := decodeItem(item)
		if !ok {
			continue
		}
		if b.Type == BlockToolUse {
			toolUse = true
		}
		content = append(content, b)
	}

	stop := "end_turn"
	switch {
	case toolUse:
		stop = "tool_use"
	case out.Status == "incomplete" && out.IncompleteDetails != nil && out.IncompleteDetails.Reason == "max_output_tokens":
		stop = "max_tokens"
	}

	return &Response{
		ID: out.ID, Model: out.Model, Role: "assistant", StopReason: stop, Content: content,
		Usage: Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens},
	}, nil
}
