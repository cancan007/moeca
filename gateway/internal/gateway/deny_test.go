package gateway

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"orchestra/gateway/internal/config"
)

func TestForbiddenCommandScope(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	cfg.Deny = []config.DenyRule{
		{Scope: "command", Pattern: "/v1/danger/*", Methods: []string{"DELETE"}, Note: "no destructive deletes"},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	// DELETE on the denied path -> 403
	resp := do(t, srv, "DELETE", "/echo/v1/danger/thing", map[string]string{SessionHeader: "tok"}, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(string(body), "policy") {
		t.Fatalf("denied command: status %d body %s, want 403 policy", resp.StatusCode, body)
	}

	// GET on the same path is allowed (method filter did not match)
	resp = do(t, srv, "GET", "/echo/v1/danger/thing", map[string]string{SessionHeader: "tok"}, "")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET on danger path: status %d, want 200 (only DELETE denied)", resp.StatusCode)
	}

	// A different path is unaffected
	resp = do(t, srv, "DELETE", "/echo/v1/safe/thing", map[string]string{SessionHeader: "tok"}, "")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("DELETE on safe path: status %d, want 200", resp.StatusCode)
	}
}

func TestForbiddenNetworkScope(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	// Block the upstream's own host (127.0.0.1) via CIDR.
	cfg.Deny = []config.DenyRule{
		{Scope: "network", Pattern: "127.0.0.0/8", Note: "no loopback"},
	}
	gw := New(cfg, io.Discard, nil, nil)
	srv := httptest.NewServer(gw)
	defer srv.Close()

	resp := do(t, srv, "GET", "/echo/v1/x", map[string]string{SessionHeader: "tok"}, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 || !strings.Contains(string(body), "policy") {
		t.Fatalf("denied network: status %d body %s, want 403 policy", resp.StatusCode, body)
	}
}
