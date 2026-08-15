package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// openAIClient speaks the OpenAI Chat Completions API (POST /v1/chat/completions)
// through the gateway. It translates the neutral Request/Response to/from
// OpenAI's message + tool_call shapes.
type openAIClient struct {
	baseURL string
	gctx    GatewayCtx
	http    *http.Client
}

type oaFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON *string*
}

type oaToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function oaFunctionCall `json:"function"`
}

// oaMessage's Content is `any` because this dialect has two shapes for it: a
// plain string, and an array of parts once an image is involved. Sending the
// array form unconditionally would change every request for the sake of the
// rare one that carries a picture.
type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

// oaImagePart is this dialect's image: a data: URI rather than a base64 field.
type oaImagePart struct {
	Type     string            `json:"type"`
	ImageURL map[string]string `json:"image_url"`
}

func oaImage(mediaType, data string) oaImagePart {
	return oaImagePart{Type: "image_url", ImageURL: map[string]string{"url": "data:" + mediaType + ";base64," + data}}
}

type oaTool struct {
	Type     string        `json:"type"`
	Function oaToolFuncDef `json:"function"`
}

type oaToolFuncDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type oaRequest struct {
	Model               string      `json:"model"`
	MaxCompletionTokens int         `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string      `json:"reasoning_effort,omitempty"`
	Messages            []oaMessage `json:"messages"`
	Tools               []oaTool    `json:"tools,omitempty"`
}

// oaEffort maps the neutral effort level onto OpenAI's reasoning_effort, which
// only accepts low|medium|high — xhigh/max clamp to high. An unset or unknown
// value returns "" so the field is omitted (the provider default applies).
func oaEffort(effort string) string {
	switch effort {
	case "low", "medium", "high":
		return effort
	case "xhigh", "max":
		return "high"
	default:
		return ""
	}
}

// reasoningModels are the OpenAI model families that accept reasoning_effort.
//
// Matched by prefix because the versioned ids move ("o3", "o3-2025-04-16") far
// faster than this file does.
var reasoningModels = []string{"o1", "o3", "o4", "gpt-5"}

// acceptsEffort reports whether a model takes the reasoning_effort argument.
//
// The chat models do not, and they do not ignore it either: gpt-4o answers a
// request carrying it with `400 Unrecognized request argument supplied:
// reasoning_effort`, which kills the stage outright. Since the agent applies a
// default effort of its own — nobody has to ask for one — every non-reasoning
// OpenAI model was unusable, and the failure named a parameter the operator had
// never set.
//
// So the field is sent only where it is known to belong. An unrecognised model
// is treated as not accepting it: dropping the effort costs some depth on a
// model that would have taken it, while sending it costs the whole run on one
// that would not.
func acceptsEffort(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, p := range reasoningModels {
		if m == p || strings.HasPrefix(m, p+"-") || strings.HasPrefix(m, p+".") {
			return true
		}
	}
	return false
}

func (c *openAIClient) encode(req Request) oaRequest {
	msgs := make([]oaMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			var calls []oaToolCall
			for _, b := range m.Content {
				if b.Type == BlockToolUse {
					args := string(b.Input)
					if args == "" {
						args = "{}"
					}
					calls = append(calls, oaToolCall{ID: b.ID, Type: "function", Function: oaFunctionCall{Name: b.Name, Arguments: args}})
				}
			}
			msgs = append(msgs, oaMessage{Role: "assistant", Content: blocksText(m.Content), ToolCalls: calls})
		default: // user
			var text []string
			var images []any
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					text = append(text, b.Text)
				case BlockToolResult:
					// A tool message cannot carry an image in this dialect, so
					// images travel in the user message below instead.
					msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content})
				case BlockImage:
					images = append(images, oaImage(b.MediaType, b.Data))
				}
			}
			switch {
			case len(images) > 0:
				parts := make([]any, 0, len(images)+1)
				if len(text) > 0 {
					parts = append(parts, map[string]string{"type": "text", "text": strings.Join(text, "\n")})
				}
				parts = append(parts, images...)
				msgs = append(msgs, oaMessage{Role: "user", Content: parts})
			case len(text) > 0:
				msgs = append(msgs, oaMessage{Role: "user", Content: strings.Join(text, "\n")})
			}
		}
	}

	var tools []oaTool
	for _, t := range req.Tools {
		// Server tools (web search) are executed by Anthropic and have no
		// equivalent here. Dropping them is the honest translation: advertising
		// the name as a function would make the model call a tool nobody runs.
		if t.IsServer() {
			continue
		}
		tools = append(tools, oaTool{Type: "function", Function: oaToolFuncDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}})
	}
	var effort string
	if req.OutputConfig != nil && acceptsEffort(req.Model) {
		effort = oaEffort(req.OutputConfig.Effort)
	}
	return oaRequest{Model: req.Model, MaxCompletionTokens: req.MaxTokens, ReasoningEffort: effort, Messages: msgs, Tools: tools}
}

type oaResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string       `json:"role"`
			Content   string       `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func (c *openAIClient) CreateMessage(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(c.encode(req))
	if err != nil {
		return nil, fmt.Errorf("openai: marshal request: %w", err)
	}
	raw, status, err := httpPostJSON(ctx, c.http, c.baseURL+"/v1/chat/completions", c.gctx, body)
	if err != nil {
		return nil, fmt.Errorf("openai: post: %w", err)
	}
	if status != http.StatusOK {
		var e oaResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != nil && e.Error.Message != "" {
			return nil, fmt.Errorf("openai: %d (%s): %s", status, e.Error.Type, e.Error.Message)
		}
		return nil, fmt.Errorf("openai: %d: %s", status, strings.TrimSpace(string(raw)))
	}

	var out oaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("openai: response had no choices")
	}
	msg := out.Choices[0].Message

	var content []Block
	if msg.Content != "" {
		content = append(content, TextBlock(msg.Content))
	}
	for _, tc := range msg.ToolCalls {
		args := tc.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		content = append(content, Block{Type: BlockToolUse, ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args)})
	}

	stop := "end_turn"
	switch {
	case len(msg.ToolCalls) > 0 || out.Choices[0].FinishReason == "tool_calls":
		stop = "tool_use"
	case out.Choices[0].FinishReason == "length":
		stop = "max_tokens"
	}

	return &Response{
		ID: out.ID, Model: out.Model, Role: "assistant", StopReason: stop, Content: content,
		Usage: Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens},
	}, nil
}
