package gateway

import (
	"io"
	"net/http/httptest"
	"testing"

	"orchestra/gateway/internal/config"
)

// pathConfig fronts the echo upstream with a service that forwards one route.
func pathConfig(upstream string, allow []string) *config.Config {
	c := baseConfig(upstream)
	svc := c.Services["echo"]
	svc.AllowPaths = allow
	c.Services["echo"] = svc
	return c
}

// The indexer's /status and /graph enumerate every source in the index. Group
// filtering guards /search only, so without a route allowlist an agent could
// read the whole catalogue through a sibling route and the permission model
// would be decorative.
func TestPrivilegedReadRoutesAreNotForwarded(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	srv := httptest.NewServer(New(pathConfig(up.URL, []string{"/search"}), io.Discard, nil, nil))
	defer srv.Close()

	if resp := do(t, srv, "POST", "/echo/search", map[string]string{SessionHeader: "tok"}, "{}"); resp.StatusCode != 200 {
		t.Errorf("the permitted route was blocked: %d", resp.StatusCode)
	}
	for _, blocked := range []string{"/echo/status", "/echo/graph", "/echo/index"} {
		if resp := do(t, srv, "GET", blocked, map[string]string{SessionHeader: "tok"}, ""); resp.StatusCode != 403 {
			t.Errorf("%s = %d, want 403 — a GET must not bypass the route allowlist", blocked, resp.StatusCode)
		}
	}
}

// The check is on the upstream-relative path, so a caller cannot smuggle a
// blocked route past it by dressing it up as the allowed prefix.
func TestRouteAllowlistIsNotPrefixMatched(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	srv := httptest.NewServer(New(pathConfig(up.URL, []string{"/search"}), io.Discard, nil, nil))
	defer srv.Close()

	for _, blocked := range []string{"/echo/search/all", "/echo/searchx"} {
		if resp := do(t, srv, "POST", blocked, map[string]string{SessionHeader: "tok"}, "{}"); resp.StatusCode != 403 {
			t.Errorf("%s = %d, want 403", blocked, resp.StatusCode)
		}
	}
}

// Services that never opt in keep forwarding their whole surface; the model
// providers need it.
func TestNoAllowlistForwardsEverything(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	srv := httptest.NewServer(New(pathConfig(up.URL, nil), io.Discard, nil, nil))
	defer srv.Close()

	if resp := do(t, srv, "POST", "/echo/v1/anything", map[string]string{SessionHeader: "tok"}, "{}"); resp.StatusCode != 200 {
		t.Errorf("unrestricted service blocked a route: %d", resp.StatusCode)
	}
}

func TestPathAllowedGlobs(t *testing.T) {
	svc := config.Service{AllowPaths: []string{"/search", "/v1/*/read"}}
	for _, c := range []struct {
		path string
		want bool
	}{
		{"/search", true},
		{"/v1/abc/read", true},
		{"/v1/abc/write", false},
		{"/v1/a/b/read", false}, // '*' spans one segment only
		{"/", false},
	} {
		if got := svc.PathAllowed(c.path); got != c.want {
			t.Errorf("PathAllowed(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
