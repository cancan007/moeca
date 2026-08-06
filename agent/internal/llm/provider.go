package llm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
)

// Provider is one LLM dialect the agent can speak, all through the gateway. The
// agent's tool-use loop is written against the neutral Request/Response/Block
// model in this package; each Provider translates that to/from a vendor's wire
// format (Anthropic Messages, OpenAI Chat Completions, Gemini generateContent).
// The gateway injects credentials, so a Provider sends only Content-Type.
type Provider interface {
	CreateMessage(ctx context.Context, req Request) (*Response, error)
}

// Provider kinds (matches ORCHESTRA_PROVIDER / the gateway service Kind).
const (
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
	KindGemini    = "gemini"
)

// Gateway headers the agent sends. SessionHeader authenticates to the gateway;
// Run/Stage carry orchestration attribution for the monitoring plane. All are
// scrubbed by the gateway before the upstream call.
const (
	SessionHeader = "X-Orchestra-Session"
	RunHeader     = "X-Orchestra-Run"
	StageHeader   = "X-Orchestra-Stage"
)

// GatewayCtx is what the agent tells the gateway about itself on every call.
type GatewayCtx struct {
	Session string
	Run     string
	Stage   string
}

// Apply sets the gateway headers this context carries.
func (g GatewayCtx) Apply(h http.Header) {
	if g.Session != "" {
		h.Set(SessionHeader, g.Session)
	}
	if g.Run != "" {
		h.Set(RunHeader, g.Run)
	}
	if g.Stage != "" {
		h.Set(StageHeader, g.Stage)
	}
}

// NewProvider builds the Provider for a dialect. base is the gateway prefix for
// that provider (e.g. http://orchestra-gateway:8787/anthropic); gctx carries the
// gateway session + run/stage attribution (may be empty in tests). Unknown kinds
// fall back to Anthropic.
func NewProvider(kind, base string, gctx GatewayCtx, httpClient *http.Client) Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}
	base = strings.TrimRight(base, "/")
	switch strings.ToLower(kind) {
	case KindOpenAI:
		return &openAIClient{baseURL: base, gctx: gctx, http: httpClient}
	case KindGemini:
		return &geminiClient{baseURL: base, gctx: gctx, http: httpClient}
	default:
		return &Client{baseURL: base, gctx: gctx, http: httpClient}
	}
}

// httpPostJSON POSTs a JSON body (the gateway injects upstream auth; the agent
// sends only Content-Type + its gateway headers) and returns the raw response
// bytes + status. Shared by the OpenAI/Gemini dialects.
func httpPostJSON(ctx context.Context, hc *http.Client, url string, gctx GatewayCtx, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	gctx.Apply(req.Header)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

// blocksText concatenates the text of all text blocks (newline-joined).
func blocksText(blocks []Block) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == BlockText && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
