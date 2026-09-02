package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// A forged header must not survive even when the session is entitled to no
// groups at all: it would otherwise be passed straight through to an upstream
// that trusts it, turning the narrowest grant into the widest.
func TestForgedGroupsAreDroppedForAGloballyScopedSession(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{"tok": {ID: "s1", Groups: []string{}}})

	present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{
		SessionHeader: "tok",
		GroupsHeader:  "admin",
	}, "{}"))
	if !present {
		t.Fatal("the session's own (empty) entitlement must still be stated")
	}
	if len(vals) != 1 || vals[0] != "" {
		t.Errorf("groups = %q, want a single empty value — the forged list must not survive", vals)
	}
}

// nil groups and an empty list are opposites, and this is where that finally
// decides something: nil never stated an entitlement and is refused outright,
// empty is the global scope and is answered with everyone's knowledge.
//
// Collapsing them would make the scope nobody chose the widest grant available,
// which is the shape the whole path exists to avoid.
func TestNilAndEmptyGroupsAreDistinct(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{
		"noscope": {ID: "s1"},
		"global":  {ID: "s2", Groups: []string{}},
	})

	res := do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "noscope"}, "{}")
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("a session that never chose a scope got %d, want 403", res.StatusCode)
	}

	present, vals := upstreamGroups(t, do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "global"}, "{}"))
	if !present {
		t.Fatal("a session entitled to no groups must still send the header, or the upstream reads it as no policy")
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

// Retrieval is closed by default: the anonymous fallback does not reach a
// service that enforces knowledge permissions.
//
// An anonymous caller carries no group policy, and no policy is exactly what
// the indexer reads as "search everything" — so the development convenience
// would hand the whole index to anyone able to reach the port. Every other
// service keeps the fallback; this one requires a session.
func TestAnonymousCallerCannotReachAKnowledgeScopedService(t *testing.T) {
	up := groupsUpstream(t)
	defer up.Close()
	cfg := groupsConfig(up.URL, nil)
	cfg.Services["fetch"] = config.Service{
		Prefix:    "/fetch/",
		Upstream:  up.URL,
		Allowlist: []string{"127.0.0.1"},
	}
	srv := httptest.NewServer(New(cfg, io.Discard, nil, nil))
	defer srv.Close()

	res := do(t, srv, "POST", "/rag/search", nil, "{}")
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous /rag/search = %d, want 401", res.StatusCode)
	}

	// The same configuration, a service that does not gate on groups: still open,
	// so this closes retrieval rather than local development as a whole.
	res = do(t, srv, "GET", "/fetch/x", nil, "")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("anonymous /fetch/x = %d, want 200", res.StatusCode)
	}
}

// A session that states an entitlement passes, whether that entitlement is a
// list of groups or the empty one the global scope resolves to. What it may
// then retrieve is the indexer's decision, not this check's.
func TestSessionedCallerWithAScopeReachesAKnowledgeScopedService(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{
		"global": {ID: "s1", Groups: []string{}},
		"team":   {ID: "s2", Groups: []string{"team-a"}},
	})

	for _, tok := range []string{"global", "team"} {
		res := do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: tok}, "{}")
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s /rag/search = %d, want 200", tok, res.StatusCode)
		}
	}
}

// The default a task is created with is "no scope", and it must retrieve
// nothing — not everything. Reaching the whole index is something a person asks
// for by choosing a scope; Global is that request, spelled out.
func TestRunWithNoScopeCannotRetrieve(t *testing.T) {
	srv := groupsServer(t, map[string]config.Session{"unscoped": {ID: "run-1"}})

	res := do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "unscoped"}, "{}")
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("unscoped /rag/search = %d, want 403", res.StatusCode)
	}
	var body struct {
		Error string `json:"error"`
	}
	json.NewDecoder(res.Body).Decode(&body)
	if !strings.Contains(body.Error, "knowledge scope") {
		t.Errorf("error = %q, want it to name the missing knowledge scope", body.Error)
	}
}

// The rule is derived from the ${GROUPS} injection rather than from a service
// name, so a service is protected by the fact that it receives entitlements.
func TestScopesKnowledgeFollowsTheGroupsTemplate(t *testing.T) {
	scoped := config.Service{InjectHeaders: map[string]string{GroupsHeader: "${GROUPS}"}}
	if !scoped.ScopesKnowledge() {
		t.Error("a service injecting ${GROUPS} must be treated as knowledge-scoped")
	}
	plain := config.Service{InjectHeaders: map[string]string{"Authorization": "Bearer ${SECRET}"}}
	if plain.ScopesKnowledge() {
		t.Error("a service with no ${GROUPS} template must not be treated as knowledge-scoped")
	}
}

// The log has to be able to answer "what was this run allowed to reach", not
// only "what did it reach". Reconstructing the grant from the graph afterwards
// answers it against the graph as it is now, which is not the graph as it was
// then — and a relation added since would make an old run look wrong.
func TestTheGrantIsRecordedInTheLog(t *testing.T) {
	var buf bytes.Buffer
	up := groupsUpstream(t)
	defer up.Close()
	cfg := groupsConfig(up.URL, map[string]config.Session{
		"tok": {ID: "run-1", Groups: []string{"grp-kon", "grp-media"}},
	})
	cfg.Services["anthropic"] = config.Service{
		Prefix: "/anthropic/", Upstream: up.URL, Allowlist: []string{"127.0.0.1"}, Kind: "model",
	}
	srv := httptest.NewServer(New(cfg, &buf, nil, nil))
	defer srv.Close()

	do(t, srv, "POST", "/rag/search", map[string]string{SessionHeader: "tok"}, "{}").Body.Close()
	do(t, srv, "POST", "/anthropic/v1/messages", map[string]string{SessionHeader: "tok"}, "{}").Body.Close()

	var rag, model []string
	seenModel := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec struct {
			Service string   `json:"service"`
			Groups  []string `json:"groups"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Service == "rag" {
			rag = rec.Groups
		}
		if rec.Service == "anthropic" {
			model, seenModel = rec.Groups, true
		}
	}
	if len(rag) != 2 || rag[0] != "grp-kon" {
		t.Errorf("the retrieval record carried groups %v, want the injected entitlement", rag)
	}
	// Only where it meant something. A model call recording a permission it never
	// consulted would make the one place it matters stop standing out.
	if !seenModel {
		t.Fatal("no record for the model call")
	}
	if model != nil {
		t.Errorf("a model call recorded groups %v", model)
	}
}

// A run refused for having stated no entitlement is the case someone will look
// for, so the record has to be attributable to that run.
func TestARefusedRunIsAttributableInTheLog(t *testing.T) {
	var buf bytes.Buffer
	up := groupsUpstream(t)
	defer up.Close()
	srv := httptest.NewServer(New(groupsConfig(up.URL, map[string]config.Session{
		"unscoped": {ID: "run-9"},
	}), &buf, nil, nil))
	defer srv.Close()

	do(t, srv, "POST", "/rag/search", map[string]string{
		SessionHeader: "unscoped",
		RunHeader:     "run-9",
		StageHeader:   "sup-plan",
	}, "{}").Body.Close()

	if !strings.Contains(buf.String(), `"err":"scope_required"`) {
		t.Fatalf("no refusal recorded: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"run":"run-9"`) || !strings.Contains(buf.String(), `"stage":"sup-plan"`) {
		t.Errorf("the refusal is not attributable to its run/stage: %s", buf.String())
	}
}
