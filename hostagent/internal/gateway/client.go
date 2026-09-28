// Package gateway is the host agent's client for the security gateway.
//
// Anything host-side that needs to reach an upstream API goes through here: the
// gateway routes by path prefix and injects the credential, so no caller in
// this process holds a key. It started life inside internal/tasksource because
// pulling tickets was the only thing that needed it; it lives here now because
// chat history compaction needs the same client to reach a model, and a package
// named "tasksource" is the wrong place to find that.
package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client issues requests through the security gateway. It is constructed with
// the gateway base URL and an ORCHESTRA_SESSION token; upstream credentials are
// the gateway's business, so callers are auth-format-agnostic.
type Client struct {
	base    string
	session string
	hc      *http.Client
}

// New builds a client. base is e.g. "http://127.0.0.1:8787".
func New(base, session string) *Client {
	return &Client{
		base:    strings.TrimRight(base, "/"),
		session: session,
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

// NewWithTimeout is New with a different deadline. A model call is not a ticket
// read: summarizing a long transcript routinely takes longer than the 20s that
// is generous for a REST fetch, and a compaction that times out mid-flight
// leaves the caller unable to tell a slow model from a broken gateway.
func NewWithTimeout(base, session string, timeout time.Duration) *Client {
	c := New(base, session)
	c.hc = &http.Client{Timeout: timeout}
	return c
}

// Configured reports whether there is a gateway to talk to at all.
func (c *Client) Configured() bool { return c != nil && c.base != "" }

// Do performs a gateway-routed request and returns the response body. A non-2xx
// status is an error (the body is included for diagnostics).
func (c *Client) Do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
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
