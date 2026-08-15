// Package llm is a minimal, stdlib-only client for Claude's Messages API,
// spoken through the Orchestra security gateway.
//
// The agent runs inside a credential-free sandbox: it never holds an API key.
// It POSTs to {BASE}/v1/messages where BASE is the gateway's Anthropic prefix
// (e.g. http://host.docker.internal:8787/anthropic). The gateway strips the
// /anthropic prefix, forwards to api.anthropic.com, and injects the
// `x-api-key` + `anthropic-version` headers on the way out. This client MUST
// therefore send only `Content-Type: application/json` — setting auth or
// version headers here would either be scrubbed or collide with the gateway's.
//
// Content blocks are modelled with a custom (Un)marshal so that block types the
// agent doesn't interpret — notably `thinking` — round-trip verbatim: an
// assistant turn is echoed back to the API exactly as received, byte-for-byte,
// which the Messages API requires for thinking/tool_use continuity.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout bounds a single Messages API call. It is deliberately SHORTER
// than the gateway's own request timeout, so that a call which runs too long is
// abandoned here — by the party that can retry — and a "deadline exceeded" in
// the log has one possible source rather than two.
//
// LLM turns are long
// (thinking + generation), so this is generous.
const DefaultTimeout = 300 * time.Second

// Client talks to the Messages API through the gateway base URL.
type Client struct {
	baseURL string
	gctx    GatewayCtx // gateway session + run/stage attribution
	http    *http.Client
}

// New returns a Client posting to base (the gateway's Anthropic prefix, without
// a trailing /v1/messages). A nil httpClient uses a sane default.
func New(base string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{baseURL: strings.TrimRight(base, "/"), http: httpClient}
}

// Thinking is the extended-thinking configuration. "adaptive" is correct for
// Claude 4.6 and later, and BudgetTokens must be omitted there (the API rejects
// it with a 400). Claude 4.5 and earlier invert this: they reject "adaptive" and
// require {type:"enabled", budget_tokens:N}. Callers ask for adaptive; the
// Anthropic client downgrades per model — see capabilities.go.
type Thinking struct {
	Type string `json:"type"`
	// BudgetTokens is only set for the legacy "enabled" form.
	BudgetTokens int `json:"budget_tokens,omitempty"`
}

// OutputConfig carries output-level generation knobs (the Messages API
// `output_config` object). Effort tunes thinking depth and overall token spend
// — low | medium | high | xhigh | max; the API default when omitted is "high".
// Lowering it is the primary cost lever on opus-4-8. Although this mirrors the
// Anthropic wire shape, it also serves as the neutral carrier the OpenAI dialect
// maps to `reasoning_effort` (Gemini has no equivalent and ignores it).
type OutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

// Tool is a tool definition advertised to the model. Two shapes share it.
//
// A CLIENT tool (the default) carries a description and an input schema, and
// the agent executes it: the model emits tool_use, the loop dispatches it and
// answers with tool_result. A SERVER tool carries a Type instead — the
// versioned Anthropic tool id, e.g. "web_search_20260209" — and the provider
// executes it: the call and its result arrive together as extra content blocks
// in the same response, and the agent is never asked to run anything.
//
// Server tools exist only in the Anthropic dialect; the OpenAI and Gemini
// encoders drop them rather than translate a call they cannot make.
type Tool struct {
	Name        string         `json:"name"`
	Type        string         `json:"type,omitempty"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`

	// Server-tool knobs (web search). Zero values are omitted, which the API
	// reads as "no cap" / "no domain filter".
	MaxUses        int      `json:"max_uses,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

// IsServer reports whether the provider executes this tool rather than the agent.
func (t Tool) IsServer() bool { return t.Type != "" }

// Message is one conversation turn. Content is a heterogeneous block list.
type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

// Request is the Messages API request body. Thinking and OutputConfig mirror the
// Anthropic wire shape but double as neutral carriers the other dialects read
// (or ignore) — see the per-dialect encoders in openai.go / gemini.go.
type Request struct {
	Model        string        `json:"model"`
	MaxTokens    int           `json:"max_tokens"`
	System       string        `json:"system,omitempty"`
	Thinking     *Thinking     `json:"thinking,omitempty"`
	OutputConfig *OutputConfig `json:"output_config,omitempty"`
	Messages     []Message     `json:"messages"`
	Tools        []Tool        `json:"tools,omitempty"`
}

// Response is the subset of the Messages API response the agent needs.
type Response struct {
	ID         string  `json:"id"`
	Model      string  `json:"model"`
	Role       string  `json:"role"`
	StopReason string  `json:"stop_reason"`
	Content    []Block `json:"content"`
	Usage      Usage   `json:"usage"`
}

// Usage is the token accounting returned with each response.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Block is a single content block. Known blocks (text, tool_use, tool_result)
// are decoded into typed fields; any other block type (e.g. thinking) is kept
// as Raw and re-emitted verbatim so it round-trips unchanged.
type Block struct {
	Type string

	// text
	Text string
	// tool_use
	ID    string
	Name  string
	Input json.RawMessage
	// tool_result
	ToolUseID string
	Content   string
	IsError   bool
	// image: base64 payload and its media type. Every dialect can take an image
	// in a user turn, so this is the one shape that lets an agent LOOK at what
	// a run produced instead of only being told a file exists.
	MediaType string
	Data      string

	// Raw holds the original JSON for block types this package does not model.
	Raw json.RawMessage
}

// Block type constants.
const (
	BlockText       = "text"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
	BlockImage      = "image"
	// BlockServerToolUse is a provider-executed tool call (web search). The
	// agent must NOT dispatch it — the provider already ran it and put the
	// result in the same response — so it stays an unmodelled block that
	// round-trips verbatim. Only its name is read out, for the run log.
	BlockServerToolUse = "server_tool_use"
)

// ServerToolName returns the tool name of a server_tool_use block, and "" for
// every other block. It reads Raw because server blocks are kept verbatim.
func (b Block) ServerToolName() string {
	if b.Type != BlockServerToolUse || len(b.Raw) == 0 {
		return ""
	}
	var t struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(b.Raw, &t) != nil {
		return ""
	}
	return t.Name
}

// TextBlock builds a text content block.
func TextBlock(text string) Block { return Block{Type: BlockText, Text: text} }

// ToolResultBlock builds a tool_result content block.
// ImageBlock carries an image for the model to look at. base64 is the only
// encoding all three dialects accept without the agent needing egress of its
// own — a URL would mean the provider fetching from somewhere, which a sandbox
// has no way to offer.
func ImageBlock(mediaType, base64Data string) Block {
	return Block{Type: BlockImage, MediaType: mediaType, Data: base64Data}
}

func ToolResultBlock(toolUseID, content string, isError bool) Block {
	return Block{Type: BlockToolResult, ToolUseID: toolUseID, Content: content, IsError: isError}
}

// UnmarshalJSON decodes a content block, preserving unknown types verbatim.
func (b *Block) UnmarshalJSON(data []byte) error {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	b.Type = probe.Type
	switch probe.Type {
	case BlockText:
		var t struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			return err
		}
		b.Text = t.Text
	case BlockToolUse:
		var t struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			return err
		}
		b.ID, b.Name, b.Input = t.ID, t.Name, t.Input
	case BlockToolResult:
		var t struct {
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
			IsError   bool   `json:"is_error"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			return err
		}
		b.ToolUseID, b.Content, b.IsError = t.ToolUseID, t.Content, t.IsError
	default:
		// Unknown block (e.g. thinking) — keep the raw bytes to re-send verbatim.
		b.Raw = append(b.Raw[:0], data...)
	}
	return nil
}

// MarshalJSON re-emits a block, echoing unknown types byte-for-byte.
func (b Block) MarshalJSON() ([]byte, error) {
	switch b.Type {
	case BlockText:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{b.Type, b.Text})
	case BlockToolUse:
		input := b.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return json.Marshal(struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{b.Type, b.ID, b.Name, input})
	case BlockToolResult:
		return json.Marshal(struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
			IsError   bool   `json:"is_error,omitempty"`
		}{b.Type, b.ToolUseID, b.Content, b.IsError})
	case BlockImage:
		// The Anthropic wire shape; the other dialects re-encode it in their
		// own encoders (see openai.go / gemini.go).
		return json.Marshal(struct {
			Type   string `json:"type"`
			Source struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			} `json:"source"`
		}{Type: b.Type, Source: struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		}{"base64", b.MediaType, b.Data}})
	default:
		if len(b.Raw) > 0 {
			return b.Raw, nil
		}
		return nil, fmt.Errorf("llm: cannot marshal block of type %q with no raw payload", b.Type)
	}
}

// apiError is the Messages API error envelope.
type apiError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// CreateMessage performs one POST /v1/messages call.
func (c *Client) CreateMessage(ctx context.Context, req Request) (*Response, error) {
	// Downgrade thinking/effort for models that predate them, so a stage pinned
	// to an older model doesn't 400 the whole run.
	req = normalize(req)
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("llm: marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: build request: %w", err)
	}
	// Content-Type + the gateway headers (the gateway injects x-api-key +
	// anthropic-version and scrubs the agent headers before forwarding upstream).
	httpReq.Header.Set("Content-Type", "application/json")
	c.gctx.Apply(httpReq.Header)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: post %s: %w", url, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var ae apiError
		if json.Unmarshal(raw, &ae) == nil && ae.Error.Message != "" {
			return nil, fmt.Errorf("llm: %s (%s): %s", resp.Status, ae.Error.Type, ae.Error.Message)
		}
		return nil, fmt.Errorf("llm: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}

	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("llm: decode response: %w", err)
	}
	return &out, nil
}
