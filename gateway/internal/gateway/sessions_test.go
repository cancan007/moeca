package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A run's retrieval scope lives in the session it presents, because the caller
// may not name its own groups: the gateway states X-Orchestra-Groups about the
// caller and discards whatever arrived. Minting is therefore the only way a
// per-run scope can exist at all.

func mint(t *testing.T, srv *httptest.Server, admin, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+AdminSessionsPath, strings.NewReader(body))
	if admin != "" {
		req.Header.Set(AdminHeader, admin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestMintedSessionCarriesItsGroups(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	gw.adminToken = "adm"
	srv := httptest.NewServer(gw)
	defer srv.Close()

	code, out := mint(t, srv, "adm", `{"id":"run-1","groups":["payments"]}`)
	if code != 201 {
		t.Fatalf("mint status = %d", code)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("no token returned")
	}

	id, groups, ok := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{token}}})
	if !ok || id != "run-1" {
		t.Fatalf("authenticate = %q %v %v", id, groups, ok)
	}
	if len(groups) != 1 || groups[0] != "payments" {
		t.Errorf("groups = %v, want [payments]", groups)
	}
}

// The token is the authorization; nothing else stands behind it, so it must not
// be mintable without the admin token a sandbox never sees.
func TestMintingRequiresTheAdminToken(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	gw.adminToken = "adm"
	srv := httptest.NewServer(gw)
	defer srv.Close()

	if code, _ := mint(t, srv, "", `{"id":"x"}`); code != 401 {
		t.Errorf("status without an admin token = %d, want 401", code)
	}
	if code, _ := mint(t, srv, "wrong", `{"id":"x"}`); code != 401 {
		t.Errorf("status with a wrong admin token = %d, want 401", code)
	}
	if gw.sessions.count() != 0 {
		t.Error("a session was minted anyway")
	}
}

// nil and empty are different scopes and the difference is load-bearing: nil is
// "no policy, search everything", empty is "entitled to nothing".
func TestNullAndEmptyGroupsStayDistinct(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	gw.adminToken = "adm"
	srv := httptest.NewServer(gw)
	defer srv.Close()

	_, a := mint(t, srv, "adm", `{"id":"none","groups":null}`)
	_, b := mint(t, srv, "adm", `{"id":"empty","groups":[]}`)

	_, gA, _ := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{a["token"].(string)}}})
	_, gB, _ := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{b["token"].(string)}}})
	if gA != nil {
		t.Errorf("null groups became %v, want nil (no policy)", gA)
	}
	if gB == nil || len(gB) != 0 {
		t.Errorf("empty groups became %v, want an empty slice (entitled to nothing)", gB)
	}
}

func TestRevokingASessionStopsIt(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	gw.adminToken = "adm"
	srv := httptest.NewServer(gw)
	defer srv.Close()

	_, out := mint(t, srv, "adm", `{"id":"run-1","groups":["a"]}`)
	token := out["token"].(string)

	req, _ := http.NewRequest("DELETE", srv.URL+AdminSessionsPath+"?token="+token, nil)
	req.Header.Set(AdminHeader, "adm")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	if _, _, ok := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{token}}}); ok {
		t.Error("a revoked session still authenticates")
	}
}

// Static sessions keep working: the run session is an addition, not a
// replacement, and an existing install must not need reconfiguring.
func TestStaticSessionsStillAuthenticate(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	gw := New(baseConfig(up.URL), io.Discard, nil, nil)
	if id, _, ok := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{"tok"}}}); !ok || id != "s1" {
		t.Errorf("static session = %q %v", id, ok)
	}
}

// An unknown token fails even where no static sessions are configured.
//
// The dev fallback is for a caller that presents nothing. Letting a token the
// gateway does not recognise fall into it would make every expired run session
// — and a gateway restart drops all of them — authenticate as a caller with no
// group policy, which the indexer reads as permission to search everything.
func TestUnknownTokenIsRejectedWithNoStaticSessions(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	cfg.Sessions = nil
	gw := New(cfg, io.Discard, nil, nil)

	if id, groups, ok := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{"stale-run-token"}}}); ok {
		t.Errorf("an unknown token authenticated as %q with groups %v", id, groups)
	}
}

// Presenting nothing still works in that same configuration: the convenience is
// kept for the case it was meant for.
func TestNoTokenStillAnonymousWithNoStaticSessions(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	cfg.Sessions = nil
	gw := New(cfg, io.Discard, nil, nil)

	id, groups, ok := gw.authenticate(&http.Request{Header: http.Header{}})
	if !ok || id != AnonymousSession {
		t.Fatalf("authenticate = %q %v", id, ok)
	}
	if groups != nil {
		t.Errorf("anonymous groups = %v, want nil", groups)
	}
}

// A revoked token behaves the same way: revocation must not degrade into the
// anonymous fallback, which would widen the caller instead of stopping it.
func TestRevokedTokenDoesNotFallBackToAnonymous(t *testing.T) {
	up := echoUpstream(t)
	defer up.Close()
	cfg := baseConfig(up.URL)
	cfg.Sessions = nil
	gw := New(cfg, io.Discard, nil, nil)
	gw.adminToken = "adm"
	srv := httptest.NewServer(gw)
	defer srv.Close()

	_, out := mint(t, srv, "adm", `{"id":"run-1","groups":["a"]}`)
	token := out["token"].(string)
	req, _ := http.NewRequest("DELETE", srv.URL+AdminSessionsPath+"?token="+token, nil)
	req.Header.Set(AdminHeader, "adm")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	if id, _, ok := gw.authenticate(&http.Request{Header: http.Header{SessionHeader: []string{token}}}); ok {
		t.Errorf("a revoked token authenticated as %q", id)
	}
}
