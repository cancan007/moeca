package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func kbServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(&Config{NoSeed: true}).Handler())
	t.Cleanup(srv.Close)
	return srv
}

// post returns the decoded body so tests can pick up generated ids.
func post(t *testing.T, srv *httptest.Server, path string, body any) (int, map[string]any) {
	t.Helper()
	resp, out := req(t, srv, "POST", path, body)
	return resp.StatusCode, out
}

func mkGroup(t *testing.T, srv *httptest.Server, name string) string {
	t.Helper()
	code, out := post(t, srv, "/knowledge/group", map[string]any{"name": name})
	if code != 201 {
		t.Fatalf("create group %q = %d", name, code)
	}
	return out["id"].(string)
}

// getGraph decodes GET /knowledge into dst, which is either knowledgeGraph or a
// raw map when a test needs to see the wire form rather than the Go values.
func getGraph(t *testing.T, srv *httptest.Server, dst any) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + "/knowledge")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /knowledge = %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatal(err)
	}
}

func graph(t *testing.T, srv *httptest.Server) knowledgeGraph {
	t.Helper()
	var g knowledgeGraph
	getGraph(t, srv, &g)
	return g
}

// An empty graph must serialise as empty arrays. A nil slice becomes JSON null,
// which the screen would map over on first render — the same crash this
// codebase has already had once from the indexer's source list.
func TestEmptyGraphSerialisesAsArrays(t *testing.T) {
	var raw map[string]json.RawMessage
	getGraph(t, kbServer(t), &raw)
	for _, k := range []string{"orgs", "projects", "groups", "relations"} {
		if string(raw[k]) != "[]" {
			t.Errorf("%s = %s, want []", k, raw[k])
		}
	}
}

// Editing only a group's projects must not clear its sources. nil means "not
// submitted", [] means "clear" — folding them together would silently revoke
// retrieval access every time the assignment screen saved.
func TestOmittedFieldDoesNotClearTheOther(t *testing.T) {
	srv := kbServer(t)
	gid := mkGroup(t, srv, "決済サービス")
	if code, _ := post(t, srv, "/knowledge/group/links", map[string]any{
		"groupId": gid, "sources": []string{"docs/a.md", "docs/b.md"},
	}); code != 200 {
		t.Fatalf("set sources = %d", code)
	}
	// Save projects only; sources are absent from the body.
	if code, _ := post(t, srv, "/knowledge/group/links", map[string]any{
		"groupId": gid, "projects": []string{},
	}); code != 200 {
		t.Fatalf("set projects = %d", code)
	}
	g := graph(t, srv)
	if len(g.Groups[0].Sources) != 2 {
		t.Errorf("sources = %v, want both to survive a projects-only save", g.Groups[0].Sources)
	}
}

// An explicit empty list must still clear, or a group could never be emptied.
func TestExplicitEmptyListClears(t *testing.T) {
	srv := kbServer(t)
	gid := mkGroup(t, srv, "A")
	post(t, srv, "/knowledge/group/links", map[string]any{"groupId": gid, "sources": []string{"a.md"}})
	post(t, srv, "/knowledge/group/links", map[string]any{"groupId": gid, "sources": []string{}})

	if g := graph(t, srv); len(g.Groups[0].Sources) != 0 {
		t.Errorf("sources = %v, want cleared", g.Groups[0].Sources)
	}
}

// The renderer has no colour for an unknown relation type and would drop the
// edge without saying so, leaving a stored relation that is invisible.
func TestUnknownRelationTypeIsRejected(t *testing.T) {
	srv := kbServer(t)
	a, b := mkGroup(t, srv, "A"), mkGroup(t, srv, "B")
	if code, _ := post(t, srv, "/knowledge/relation", map[string]any{
		"from": a, "to": b, "type": "somehow-related",
	}); code != 400 {
		t.Errorf("unknown type = %d, want 400", code)
	}
	for typ := range RelationTypes {
		if code, _ := post(t, srv, "/knowledge/relation", map[string]any{"from": a, "to": b, "type": typ}); code != 201 {
			t.Errorf("type %q = %d, want 201", typ, code)
		}
	}
}

// A dangling endpoint would draw an edge to nothing, and during retrieval would
// expand to a group that does not exist.
func TestRelationToUnknownGroupIs404(t *testing.T) {
	srv := kbServer(t)
	a := mkGroup(t, srv, "A")
	if code, _ := post(t, srv, "/knowledge/relation", map[string]any{
		"from": a, "to": "grp-nope", "type": "requires",
	}); code != 404 {
		t.Errorf("dangling relation = %d, want 404", code)
	}
}

// A self-relation renders as nothing and expands to what the caller already
// has, so it is a mistake rather than a degenerate case to store.
func TestSelfRelationIsRejected(t *testing.T) {
	srv := kbServer(t)
	a := mkGroup(t, srv, "A")
	if code, _ := post(t, srv, "/knowledge/relation", map[string]any{
		"from": a, "to": a, "type": "requires",
	}); code != 400 {
		t.Errorf("self relation = %d, want 400", code)
	}
}

// A project under a missing organization would never appear in the tree.
func TestProjectUnderUnknownOrgIs404(t *testing.T) {
	srv := kbServer(t)
	if code, _ := post(t, srv, "/knowledge/project", map[string]any{
		"name": "決済基盤", "orgId": "org-nope",
	}); code != 404 {
		t.Errorf("unknown org = %d, want 404", code)
	}
}

// Renaming must not mint a new id: the id is the permission tag.
func TestRenamingGroupKeepsItsID(t *testing.T) {
	srv := kbServer(t)
	gid := mkGroup(t, srv, "決済サービス")
	if code, _ := post(t, srv, "/knowledge/group", map[string]any{"id": gid, "name": "決済ドメイン"}); code != 200 {
		t.Fatalf("rename = %d", code)
	}
	g := graph(t, srv)
	if len(g.Groups) != 1 {
		t.Fatalf("rename created a second group: %+v", g.Groups)
	}
	if g.Groups[0].ID != gid {
		t.Errorf("id = %q, want the original %q", g.Groups[0].ID, gid)
	}
}

// The whole graph comes back in one response; the screen needs all four parts
// on load.
func TestGraphRoundTrip(t *testing.T) {
	srv := kbServer(t)
	_, org := post(t, srv, "/knowledge/org", map[string]any{"name": "Acme"})
	oid := org["id"].(string)
	code, prj := post(t, srv, "/knowledge/project", map[string]any{"name": "決済基盤", "orgId": oid})
	if code != 201 {
		t.Fatalf("create project = %d", code)
	}
	pid := prj["id"].(string)
	a, b := mkGroup(t, srv, "仕様書"), mkGroup(t, srv, "過去障害")
	post(t, srv, "/knowledge/group/links", map[string]any{
		"groupId": a, "projects": []string{pid}, "sources": []string{"docs/spec.md"},
	})
	post(t, srv, "/knowledge/relation", map[string]any{"from": a, "to": b, "type": "references"})

	g := graph(t, srv)
	if len(g.Orgs) != 1 || len(g.Projects) != 1 || len(g.Groups) != 2 || len(g.Relations) != 1 {
		t.Fatalf("graph = %d orgs, %d projects, %d groups, %d relations",
			len(g.Orgs), len(g.Projects), len(g.Groups), len(g.Relations))
	}
	if g.Projects[0].OrgID != oid {
		t.Errorf("project org = %q, want %q", g.Projects[0].OrgID, oid)
	}
	var spec *struct{ P, S []string }
	for _, gr := range g.Groups {
		if gr.ID == a {
			spec = &struct{ P, S []string }{gr.Projects, gr.Sources}
		}
	}
	if spec == nil || len(spec.P) != 1 || len(spec.S) != 1 {
		t.Errorf("group links did not round-trip: %+v", spec)
	}
}

// Deleting through the API must take the dependent rows with it, the same way
// the store does — this exercises the wiring, not just the SQL.
func TestDeleteGroupRemovesItsRelations(t *testing.T) {
	srv := kbServer(t)
	a, b := mkGroup(t, srv, "A"), mkGroup(t, srv, "B")
	post(t, srv, "/knowledge/relation", map[string]any{"from": a, "to": b, "type": "requires"})

	resp, _ := req(t, srv, "DELETE", "/knowledge/group?id="+a, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	if g := graph(t, srv); len(g.Relations) != 0 {
		t.Errorf("relations = %+v, want none after an endpoint was deleted", g.Relations)
	}
}

func TestDeleteUnknownIs404(t *testing.T) {
	srv := kbServer(t)
	for _, p := range []string{"/knowledge/org?id=x", "/knowledge/project?id=x", "/knowledge/group?id=x", "/knowledge/relation?id=x"} {
		if resp, _ := req(t, srv, "DELETE", p, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("DELETE %s = %d, want 404", p, resp.StatusCode)
		}
	}
}
