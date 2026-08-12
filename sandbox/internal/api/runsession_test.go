package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A run's knowledge scope lives on a gateway session minted for it, because the
// gateway states X-Orchestra-Groups about the caller and discards whatever the
// caller sent. A sandbox that could name its own groups could name any of them.

// gatewayStub stands in for the gateway's loopback admin API.
func gatewayStub(t *testing.T, mint func(w http.ResponseWriter, body map[string]any)) (*httptest.Server, *[]string) {
	t.Helper()
	var revoked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_gateway/sessions" {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodDelete {
			revoked = append(revoked, r.URL.Query().Get("token"))
			w.WriteHeader(200)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &revoked
}

func scopedServer(t *testing.T, gw *httptest.Server, fake *fakeDocker) *httptest.Server {
	t.Helper()
	t.Setenv("ORCHESTRA_ADMIN_TOKEN", "adm")
	cfg := imagesConfig(t)
	cfg.GatewayAdminBase = gw.URL
	return newTest(cfg, fake)
}

func TestAScopedRunAuthenticatesWithItsOwnSession(t *testing.T) {
	var sawAdmin, sawGroups string
	gw, revoked := gatewayStub(t, func(w http.ResponseWriter, body map[string]any) {
		g, _ := json.Marshal(body["groups"])
		sawGroups = string(g)
		w.WriteHeader(201)
		w.Write([]byte(`{"token":"run-abc"}`))
	})
	fake := &fakeDocker{}
	srv := scopedServer(t, gw, fake)
	defer srv.Close()

	req := chainReq(stage("a"))
	req["groups"] = []string{"payments"}
	id, code := startRun(t, srv, req)
	if code != 201 {
		t.Fatalf("run status = %d", code)
	}
	waitRun(t, srv, id)

	if sawGroups != `["payments"]` {
		t.Errorf("groups sent to the gateway = %s", sawGroups)
	}
	fake.mu.Lock()
	got := fake.created[0].Env["ORCHESTRA_SESSION"]
	fake.mu.Unlock()
	if got != "run-abc" {
		t.Errorf("stage session = %q, want the minted one (not the shared token)", got)
	}
	// The session has no use once the containers are gone.
	if len(*revoked) != 1 || (*revoked)[0] != "run-abc" {
		t.Errorf("revoked = %v, want [run-abc]", *revoked)
	}
	_ = sawAdmin
}

// A run that asks for no scope behaves exactly as every run did before this
// existed: the shared session, and nothing minted.
func TestAnUnscopedRunUsesTheSharedSession(t *testing.T) {
	minted := 0
	gw, _ := gatewayStub(t, func(w http.ResponseWriter, _ map[string]any) {
		minted++
		w.WriteHeader(201)
		w.Write([]byte(`{"token":"x"}`))
	})
	fake := &fakeDocker{}
	srv := scopedServer(t, gw, fake)
	defer srv.Close()

	id, _ := startRun(t, srv, chainReq(stage("a")))
	waitRun(t, srv, id)

	if minted != 0 {
		t.Errorf("minted %d sessions for a run with no scope", minted)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := fake.created[0].Env["ORCHESTRA_SESSION"]; got != "sess" {
		t.Errorf("stage session = %q, want the shared token", got)
	}
}

// If the scope cannot be established the run must not start. Falling back to
// the shared session would hand it every group — the opposite of a scope.
func TestAScopedRunRefusesToStartWhenMintingFails(t *testing.T) {
	gw, _ := gatewayStub(t, func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"nope"}`))
	})
	fake := &fakeDocker{}
	srv := scopedServer(t, gw, fake)
	defer srv.Close()

	req := chainReq(stage("a"))
	req["groups"] = []string{"payments"}
	_, code := startRun(t, srv, req)
	if code == 201 {
		t.Fatal("the run started without the scope it asked for")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 0 {
		t.Error("a container was started anyway")
	}
}

// "Entitled to nothing" is a real scope and must survive as an empty list, not
// become "no policy".
func TestAnEmptyScopeIsSentAsEmptyNotNull(t *testing.T) {
	var sawGroups string
	gw, _ := gatewayStub(t, func(w http.ResponseWriter, body map[string]any) {
		g, _ := json.Marshal(body["groups"])
		sawGroups = string(g)
		w.WriteHeader(201)
		w.Write([]byte(`{"token":"run-empty"}`))
	})
	srv := scopedServer(t, gw, &fakeDocker{})
	defer srv.Close()

	req := chainReq(stage("a"))
	req["groups"] = []string{}
	id, code := startRun(t, srv, req)
	if code != 201 {
		t.Fatalf("run status = %d", code)
	}
	waitRun(t, srv, id)
	if sawGroups != "[]" {
		t.Errorf("groups = %s, want [] (entitled to nothing)", sawGroups)
	}
}

// Without the admin token there is no way to establish a scope, and pretending
// otherwise would run unscoped.
func TestAScopedRunRefusesWithoutAnAdminToken(t *testing.T) {
	t.Setenv("ORCHESTRA_ADMIN_TOKEN", "")
	srv := newTest(imagesConfig(t), &fakeDocker{})
	defer srv.Close()

	req := chainReq(stage("a"))
	req["groups"] = []string{"payments"}
	_, code := startRun(t, srv, req)
	if code != 400 {
		t.Errorf("status = %d, want 400", code)
	}
	_ = strings.TrimSpace("")
}

// How far a stage may follow knowledge relations belongs to its agent template,
// so two stages of one run can hold different entitlements — and must not share
// a session.
func TestStagesWithDifferentScopesGetDifferentSessions(t *testing.T) {
	var minted []string
	gw, revoked := gatewayStub(t, func(w http.ResponseWriter, body map[string]any) {
		g, _ := json.Marshal(body["groups"])
		minted = append(minted, string(g))
		w.WriteHeader(201)
		w.Write([]byte(`{"token":"tok-` + itoa(len(minted)) + `"}`))
	})
	fake := &fakeDocker{}
	srv := scopedServer(t, gw, fake)
	defer srv.Close()

	narrow := stage("plan")
	narrow["groups"] = []string{"core"}
	wide := stage("research", "plan")
	wide["groups"] = []string{"core", "related"}
	req := chainReq(narrow, wide)
	req["groups"] = []string{"core"}

	id, code := startRun(t, srv, req)
	if code != 201 {
		t.Fatalf("run status = %d", code)
	}
	waitRun(t, srv, id)

	fake.mu.Lock()
	byTask := map[string]string{}
	for _, spec := range fake.created {
		byTask[spec.TaskID] = spec.Env["ORCHESTRA_SESSION"]
	}
	fake.mu.Unlock()

	var planSess, researchSess string
	for task, sess := range byTask {
		if strings.HasSuffix(task, "-plan") {
			planSess = sess
		}
		if strings.HasSuffix(task, "-research") {
			researchSess = sess
		}
	}
	if planSess == "" || researchSess == "" {
		t.Fatalf("stage sessions = %v", byTask)
	}
	if planSess == researchSess {
		t.Errorf("both stages shared %q despite different scopes", planSess)
	}
	// Everything minted is retired.
	if len(*revoked) < 2 {
		t.Errorf("revoked = %v, want every minted session", *revoked)
	}
}

// Stages asking for the same groups share one session rather than minting one
// each: the entitlement is the same, so the capability may be too.
func TestStagesWithTheSameScopeShareASession(t *testing.T) {
	mints := 0
	gw, _ := gatewayStub(t, func(w http.ResponseWriter, _ map[string]any) {
		mints++
		w.WriteHeader(201)
		w.Write([]byte(`{"token":"tok-` + itoa(mints) + `"}`))
	})
	fake := &fakeDocker{}
	srv := scopedServer(t, gw, fake)
	defer srv.Close()

	a := stage("a")
	a["groups"] = []string{"core"}
	b := stage("b", "a")
	b["groups"] = []string{"core"}
	req := chainReq(a, b)

	id, code := startRun(t, srv, req)
	if code != 201 {
		t.Fatalf("run status = %d", code)
	}
	waitRun(t, srv, id)

	if mints != 1 {
		t.Errorf("minted %d sessions for one distinct scope", mints)
	}
}
