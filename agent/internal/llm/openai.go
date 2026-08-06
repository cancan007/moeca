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

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
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
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					text = append(text, b.Text)
				case BlockToolResult:
					msgs = append(msgs, oaMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: b.Content})
				}
			}
			if len(text) > 0 {
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
	if req.OutputConfig != nil {
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
