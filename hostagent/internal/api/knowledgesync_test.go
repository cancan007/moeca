package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The push exists so the permission model does not depend on a screen being
// open. These tests drive it directly, because a Server backed by an in-memory
// store deliberately never pushes — see the ephemeral guard.

func TestKnowledgeGroupMapCollectsEverySourcesGroups(t *testing.T) {
	s := New(&Config{NoSeed: true})
	a, err := s.store.AddKnowledgeGroup("finance", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.AddKnowledgeGroup("legal", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetKnowledgeGroupSources(a.ID, []string{"payroll.md", "shared.md"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetKnowledgeGroupSources(b.ID, []string{"shared.md"}); err != nil {
		t.Fatal(err)
	}

	m, err := s.knowledgeGroupMap()
	if err != nil {
		t.Fatal(err)
	}
	if len(m["payroll.md"]) != 1 || m["payroll.md"][0] != a.ID {
		t.Errorf("payroll.md = %v, want [%s]", m["payroll.md"], a.ID)
	}
	// A source in two groups carries both: the indexer permits a chunk matching
	// any one of them, so dropping either would silently revoke access.
	if len(m["shared.md"]) != 2 {
		t.Errorf("shared.md = %v, want both groups", m["shared.md"])
	}
}

// An empty graph must be sent as an empty map, not skipped. Clearing the last
// assignment is exactly what that looks like, and skipping it would leave the
// indexer honouring a grant nobody holds any more.
func TestKnowledgeGroupMapIsNeverNil(t *testing.T) {
	s := New(&Config{NoSeed: true})
	m, err := s.knowledgeGroupMap()
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("an empty graph produced a nil map")
	}
	if len(m) != 0 {
		t.Errorf("empty graph produced %v", m)
	}
}

func TestSyncPostsTheMappingToTheIndexer(t *testing.T) {
	var got struct {
		Map map[string][]string `json:"map"`
	}
	hit := 0
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit++
		if r.URL.Path != "/groups" || r.Method != http.MethodPost {
			t.Errorf("indexer got %s %s, want POST /groups", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
		w.Write([]byte(`{"sources":1,"matched":1}`))
	}))
	defer idx.Close()

	s := New(&Config{NoSeed: true, Rag: RagConfig{URL: idx.URL}})
	g, err := s.store.AddKnowledgeGroup("finance", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetKnowledgeGroupSources(g.ID, []string{"payroll.md"}); err != nil {
		t.Fatal(err)
	}
	// The guard is on the store, not the URL, so lift it for this one call.
	s.ephemeral = false
	if err := s.syncKnowledgeGroups(); err != nil {
		t.Fatalf("syncKnowledgeGroups: %v", err)
	}
	if hit != 1 {
		t.Fatalf("indexer hit %d times, want 1", hit)
	}
	if len(got.Map["payroll.md"]) != 1 || got.Map["payroll.md"][0] != g.ID {
		t.Errorf("pushed %v, want payroll.md -> [%s]", got.Map, g.ID)
	}
}

// An indexer that is down must not fail the caller: the graph edit that
// triggered the push already succeeded, and the run-time push is what corrects
// a missed one.
func TestSyncReportsButDoesNotPanicWhenTheIndexerIsDown(t *testing.T) {
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"building"}`))
	}))
	defer idx.Close()

	s := New(&Config{NoSeed: true, Rag: RagConfig{URL: idx.URL}})
	s.ephemeral = false
	if err := s.syncKnowledgeGroups(); err == nil {
		t.Error("a 503 from the indexer should be reported to the caller")
	}
	s.syncKnowledgeGroupsLogged("test") // must not panic
}

// The default keeps the loopback indexer, so nothing has to be configured for
// the push to work in the shipped stack.
func TestRagURLDefaultsToLoopback(t *testing.T) {
	if got := (&Config{}).ragURL(); got != "http://127.0.0.1:8790" {
		t.Errorf("ragURL() = %q", got)
	}
	if got := (&Config{Rag: RagConfig{URL: "http://x:1"}}).ragURL(); got != "http://x:1" {
		t.Errorf("ragURL() = %q, want the configured value", got)
	}
}

// A server with no file behind its store must never push: a test run on a
// machine where the app is up would otherwise replace real membership with an
// empty map.
func TestAnEphemeralStoreNeverPushes(t *testing.T) {
	hit := 0
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit++
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer idx.Close()

	s := New(&Config{NoSeed: true, Rag: RagConfig{URL: idx.URL}})
	if err := s.syncKnowledgeGroups(); err != nil {
		t.Fatal(err)
	}
	if hit != 0 {
		t.Errorf("an in-memory graph was pushed to the indexer (%d calls)", hit)
	}
}
