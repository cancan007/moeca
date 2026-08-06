package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"orchestra/gateway/internal/config"
)

// groupsUpstream reports the groups header exactly as it arrived, keeping the
// three states distinguishable: absent, empty, or a list.
func groupsUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vals, present := r.Header[http.CanonicalHeaderKey(GroupsHeader)]
		out := map[string]any{"present": present, "values": vals}
		json.NewEncoder(w).Encode(out)
	}))
}

// groupsConfig wires one service that asks for ${GROUPS} and two sessions: one
// with groups, one with none declared.
func groupsConfig(upstream string, sessions map[string]config.Session) *config.Config {
	return &config.Config{
		Listen:              "127.0.0.1:0",
		MaxBodyBytes:        8 << 20,
		AllowPrivateTargets: true,
		AdminToken:          "admintok",
		Sessions:            sessions,
		Services: map[string]config.Service{
			"rag": {
				Prefix:        "/rag/",
				Upstream:      upstream,
				Allowlist:     []string{"127.0.0.1"},
				InjectHeaders: map[string]string{GroupsHeader: "${GROUPS}"},
			},
		},
	}
}

func upstreamGroups(t *testing.T, resp *http.Response) (present bool, values []string) {
	t.Helper()
	var out struct {
		Present bool     `json:"present"`
		Values  []string `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return out.Present, out.Values
}

func groupsServer(t *testing.T, sessions map[string]config.Session) *httptest.Server {
	t.Helper()
	up := groupsUpstream(t)
	t.Cleanup(up.Close)
	srv := httptest.NewServer(New(groupsConfig(up.URL, sessions), io.Discard, nil, nil))
	t.Cleanup(srv.Close)
	return srv
}

// The session's own groups reach the upstream.
func TestSessionGroupsAreInjected(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{
		"tok": {ID: "s1", Groups: []string{"team-a", "shared"}},
	})

	present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "tok"}, "{}"))
	if !present {
		t.Fatal("groups header not sent for a session that declares groups")
	}
	if len(vals) != 1 || vals[0] != "team-a,shared" {
		t.Errorf("groups = %q, want one comma-joined value", vals)
	}
}

// This is the property the whole design rests on: an agent cannot widen its own
// entitlements by asserting them. Whatever it sends is discarded and replaced.
func TestClientSuppliedGroupsAreOverridden(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{
		"tok": {ID: "s1", Groups: []string{"team-a"}},
	})

	present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{
		SessionHeader: "tok",
		GroupsHeader:  "team-b,admin,everything",
	}, "{}"))
	if !present {
		t.Fatal("groups header missing")
	}
	if len(vals) != 1 || vals[0] != "team-a" {
		t.Errorf("groups = %q, want the session's own [team-a] — a caller must not speak for its entitlements", vals)
	}
}

// A forged header must not survive even when the session declares no policy: it
// would otherwise be passed straight through to an upstream that trusts it.
func TestForgedGroupsAreDroppedWhenSessionHasNoPolicy(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{"tok": {ID: "s1"}})

	present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{
		SessionHeader: "tok",
		GroupsHeader:  "admin",
	}, "{}"))
	if present {
		t.Errorf("a forged groups header reached the upstream: %q", vals)
	}
}

// nil groups and an empty list are opposites, and the wire must keep them
// apart: omitted means "no policy, search everything", empty means "entitled to
// nothing". Collapsing them would hand an unentitled session the whole index.
func TestNilAndEmptyGroupsAreDistinct(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{
		"nopolicy": {ID: "s1"},
		"nothing":  {ID: "s2", Groups: []string{}},
	})

	if present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "nopolicy"}, "{}")); present {
		t.Errorf("a session with no declared policy sent groups %q; the header must be omitted", vals)
	}
	present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "nothing"}, "{}"))
	if !present {
		t.Fatal("a session entitled to nothing must still send the header, or the upstream reads it as no policy")
	}
	if len(vals) != 1 || vals[0] != "" {
		t.Errorf("groups = %q, want a single empty value", vals)
	}
}

// The JSON boundary has to preserve the same distinction, since sessions are
// configured rather than constructed in Go.
func TestSessionGroupsDecodeNilVersusEmpty(t *testing.T) {
	var withNil, withEmpty config.Session
	if err := json.Unmarshal([]byte(`{"id":"a"}`), &withNil); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"id":"a","groups":[]}`), &withEmpty); err != nil {
		t.Fatal(err)
	}
	if withNil.Groups != nil {
		t.Errorf("an absent groups key must decode to nil, got %#v", withNil.Groups)
	}
	if withEmpty.Groups == nil {
		t.Error("an explicit [] must not decode to nil — it means entitled to nothing")
	}
}

// ${GROUPS} is opt-in per service so group names do not leak to model providers
// that have no use for them.
func TestGroupsAreNotSentToServicesThatDoNotAskForThem(t *testing.T) {
	up := groupsUpstream(t)
	defer up.Close()
	cfg := groupsConfig(up.URL, map[string]config.Session{"tok": {ID: "s1", Groups: []string{"team-a"}}})
	svc := cfg.Services["rag"]
	svc.InjectHeaders = nil // a provider that never asked for groups
	cfg.Services["rag"] = svc

	srv := httptest.NewServer(New(cfg, io.Discard, nil, nil))
	defer srv.Close()

	if present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "tok"}, "{}")); present {
		t.Errorf("groups %q leaked to a service that did not request them", vals)
	}
}
