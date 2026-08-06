package tasksource

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GatewayClient issues an adapter's reads through the security gateway. It is
// constructed with the gateway base URL and an ORCHESTRA_SESSION token; the
// gateway routes by path prefix and injects upstream credentials, so adapters
// carry no keys and are auth-format-agnostic.
type GatewayClient struct {
	base    string
	session string
	hc      *http.Client
}

// NewGatewayClient builds a client. base is e.g. "http://127.0.0.1:8787".
func NewGatewayClient(base, session string) *GatewayClient {
	return &GatewayClient{
		base:    strings.TrimRight(base, "/"),
		session: session,
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

// Do performs a gateway-routed request and returns the response body. A non-2xx
// status is an error (the body is included for diagnostics).
func (c *GatewayClient) Do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.session != "" {
		req.Header.Set("X-Orchestra-Session", c.session)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		snippet := string(out)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("gateway %s %s: %d %s", method, path, resp.StatusCode, snippet)
	}
	return out, nil
}
