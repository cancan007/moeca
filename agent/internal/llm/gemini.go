package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// geminiClient speaks Google's Gemini generateContent API through the gateway
// (POST /v1beta/models/{model}:generateContent). It translates the neutral
// Request/Response to/from Gemini's contents + functionCall/functionResponse
// shapes. Gemini matches tool responses by function *name* (no call id), so the
// neutral tool_use_id is resolved back to a name via the running assistant
// turns (progressively, so ids reused across turns stay correct).
type geminiClient struct {
	baseURL string
	gctx    GatewayCtx
	http    *http.Client
}

type gemFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type gemFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gemPart struct {
	Text             string               `json:"text,omitempty"`
	FunctionCall     *gemFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *gemFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *gemInlineData       `json:"inlineData,omitempty"`
}

// gemInlineData is this dialect's image: mimeType + base64, inline.
type gemInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []gemPart `json:"parts"`
}

type gemFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type gemTool struct {
	FunctionDeclarations []gemFuncDecl `json:"functionDeclarations"`
}

type gemRequest struct {
	Contents          []gemContent `json:"contents"`
	SystemInstruction *gemContent  `json:"systemInstruction,omitempty"`
	Tools             []gemTool    `json:"tools,omitempty"`
	GenerationConfig  struct {
		MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	} `json:"generationConfig"`
}

func (c *geminiClient) encode(req Request) gemRequest {
	var contents []gemContent
	idName := map[string]string{} // tool_use id -> function name, updated progressively

	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			var parts []gemPart
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					if b.Text != "" {
						parts = append(parts, gemPart{Text: b.Text})
					}
				case BlockToolUse:
					idName[b.ID] = b.Name
					args := b.Input
					if len(args) == 0 {
						args = json.RawMessage("{}")
					}
					parts = append(parts, gemPart{FunctionCall: &gemFunctionCall{Name: b.Name, Args: args}})
				}
			}
			contents = append(contents, gemContent{Role: "model", Parts: parts})
		default: // user
			var parts []gemPart
			for _, b := range m.Content {
				switch b.Type {
				case BlockText:
					parts = append(parts, gemPart{Text: b.Text})
				case BlockToolResult:
					parts = append(parts, gemPart{FunctionResponse: &gemFunctionResponse{
						Name:     idName[b.ToolUseID],
						Response: map[string]any{"result": b.Content},
					}})
				case BlockImage:
					parts = append(parts, gemPart{InlineData: &gemInlineData{MimeType: b.MediaType, Data: b.Data}})
				}
			}
			contents = append(contents, gemContent{Role: "user", Parts: parts})
		}
	}

	out := gemRequest{Contents: contents}
	out.GenerationConfig.MaxOutputTokens = req.MaxTokens
	if req.System != "" {
		out.SystemInstruction = &gemContent{Parts: []gemPart{{Text: req.System}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]gemFuncDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			// Server tools (web search) are executed by Anthropic; this dialect
			// has no equivalent, so they are dropped rather than declared as
			// functions the agent would then be asked to run.
			if t.IsServer() {
				continue
			}
			decls = append(decls, gemFuncDecl{Name: t.Name, Description: t.Description, Parameters: t.InputSchema})
		}
		if len(decls) > 0 {
			out.Tools = []gemTool{{FunctionDeclarations: decls}}
		}
	}
	return out
}

type gemResponse struct {
	Candidates []struct {
		Content struct {
			Parts []gemPart `json:"parts"`
			Role  string    `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *geminiClient) CreateMessage(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(c.encode(req))
	if err != nil {
		return nil, fmt.Errorf("gemini: marshal request: %w", err)
	}
	url := c.baseURL + "/v1beta/models/" + req.Model + ":generateContent"
	raw, status, err := httpPostJSON(ctx, c.http, url, c.gctx, body)
	if err != nil {
		return nil, fmt.Errorf("gemini: post: %w", err)
	}
	if status != http.StatusOK {
		var e gemResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != nil && e.Error.Message != "" {
			return nil, fmt.Errorf("gemini: %d: %s", status, e.Error.Message)
		}
		return nil, fmt.Errorf("gemini: %d: %s", status, strings.TrimSpace(string(raw)))
	}

	var out gemResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}
	if len(out.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: response had no candidates")
	}
	cand := out.Candidates[0]

	var content []Block
	calls := 0
	for _, p := range cand.Content.Parts {
		switch {
		case p.FunctionCall != nil:
			calls++
			args := p.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			content = append(content, Block{
				Type:  BlockToolUse,
				ID:    fmt.Sprintf("call_%d", calls),
				Name:  p.FunctionCall.Name,
				Input: args,
			})
		case p.Text != "":
			content = append(content, TextBlock(p.Text))
		}
	}

	stop := "end_turn"
	switch {
	case calls > 0:
		stop = "tool_use"
	case cand.FinishReason == "MAX_TOKENS":
		stop = "max_tokens"
	}

	return &Response{
		ID: "gemini", Model: req.Model, Role: "assistant", StopReason: stop, Content: content,
		Usage: Usage{InputTokens: out.UsageMetadata.PromptTokenCount, OutputTokens: out.UsageMetadata.CandidatesTokenCount},
	}, nil
}
