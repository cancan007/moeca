package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/ragindex/internal/index"
)

// The route, end to end: the header the gateway injects is the only thing that
// decides what comes back, exactly as it is for a search.

func sourceServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handbook.md"), []byte("alpha handbook body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payroll.md"), []byte("alpha payroll body"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := index.New(index.Config{
		Sources:   []index.SourceSpec{{Kind: index.KindLocal, Root: dir}},
		EmbedMode: index.EmbedModeOffline,
	})
	if err := idx.Build(t.Context()); err != nil {
		t.Fatal(err)
	}
	idx.SetGroups(map[string][]string{"payroll.md": {"finance"}})
	srv := httptest.NewServer(New(idx, "127.0.0.1:0").Handler())
	t.Cleanup(srv.Close)
	return srv
}

func postSource(t *testing.T, srv *httptest.Server, body string, groups ...string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/source", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, g := range groups {
		req.Header.Add(GroupsHeader, g)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSourceRouteReturnsTextForAGrantedCaller(t *testing.T) {
	srv := sourceServer(t)
	res := postSource(t, srv, `{"source":"payroll.md"}`, "finance")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var out struct{ Text string }
	json.NewDecoder(res.Body).Decode(&out)
	if !strings.Contains(out.Text, "alpha payroll body") {
		t.Errorf("text = %q", out.Text)
	}
}

// The property that matters: naming a source is not a way past the filter.
func TestSourceRouteRefusesASourceTheCallerCannotSearch(t *testing.T) {
	srv := sourceServer(t)
	res := postSource(t, srv, `{"source":"payroll.md"}`, "team-a")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("status = %d, want 404 for a source outside the caller's scope", res.StatusCode)
	}

	// Same request, same 404, for a source that does not exist — so the status
	// cannot be used to enumerate what is out of reach.
	res = postSource(t, srv, `{"source":"nothing-here.md"}`, "team-a")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("status = %d for a missing source, want the same 404", res.StatusCode)
	}
}

// An empty header is a run entitled to no groups: it still reaches what is
// everyone's, and nothing else. Absent would be no policy at all.
func TestSourceRouteHonoursTheEmptyHeader(t *testing.T) {
	srv := sourceServer(t)
	res := postSource(t, srv, `{"source":"handbook.md"}`, "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("unassigned source = %d, want 200 — it is everyone's", res.StatusCode)
	}
	res = postSource(t, srv, `{"source":"payroll.md"}`, "")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("assigned source = %d, want 404 for a caller granted nothing", res.StatusCode)
	}
}

func TestSourceRouteReturnsRawBytes(t *testing.T) {
	srv := sourceServer(t)
	res := postSource(t, srv, `{"source":"handbook.md","as":"raw"}`, "team-a")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	b := make([]byte, 64)
	n, _ := res.Body.Read(b)
	if string(b[:n]) != "alpha handbook body" {
		t.Errorf("body = %q", b[:n])
	}
}

func TestSourceRouteRejectsAnEmptySource(t *testing.T) {
	srv := sourceServer(t)
	res := postSource(t, srv, `{"source":"  "}`)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}
