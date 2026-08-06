package index

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	got := chunkText("line one\nline two\nline three", 12)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks for small window, got %d: %v", len(got), got)
	}
	if chunkText("   ", 100) != nil {
		t.Errorf("blank text should produce no chunks")
	}
}

func TestCosine(t *testing.T) {
	if c := cosine([]float32{1, 0}, []float32{1, 0}); c < 0.999 {
		t.Errorf("identical vectors cosine = %v, want ~1", c)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); c > 0.001 {
		t.Errorf("orthogonal vectors cosine = %v, want ~0", c)
	}
}

// mockGateway returns [1,0] for texts mentioning "alpha", else [0,1], so search
// relevance is deterministic.
func mockGateway(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/embeddings" {
			t.Errorf("unexpected embed path %s", r.URL.Path)
		}
		if r.Header.Get("X-Orchestra-Session") != "sess" {
			t.Errorf("missing session header")
		}
		body, _ := io.ReadAll(r.Body)
		var in struct {
			Input []string `json:"input"`
		}
		json.Unmarshal(body, &in)
		var out struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		for _, s := range in.Input {
			v := []float32{0, 1}
			if strings.Contains(strings.ToLower(s), "alpha") {
				v = []float32{1, 0}
			}
			out.Data = append(out.Data, struct {
				Embedding []float32 `json:"embedding"`
			}{v})
		}
		json.NewEncoder(w).Encode(out)
	}))
}

func TestStripHTML(t *testing.T) {
	in := "<html><body><h1>Title</h1><script>evil()</script><p>hello&nbsp;world</p></body></html>"
	out := stripHTML(in)
	if strings.Contains(out, "evil") || strings.Contains(out, "<") {
		t.Errorf("stripHTML left markup/script: %q", out)
	}
	if !strings.Contains(out, "Title") || !strings.Contains(out, "hello world") {
		t.Errorf("stripHTML dropped text: %q", out)
	}
}

// The default is global: a source that was just registered belongs to no group
// yet, and a narrower default would make it invisible to every scoped search —
// indexed, present, and unreachable, with nothing in the UI to explain why.
func TestNormalizeScope(t *testing.T) {
	cases := map[string]string{
		"org": ScopeOrganization, "organization": ScopeOrganization,
		"manual": ScopeManual, "project": ScopeProject,
		"global": ScopeGlobal, "": ScopeGlobal, "weird": ScopeGlobal,
	}
	for in, want := range cases {
		if got := normalizeScope(in); got != want {
			t.Errorf("normalizeScope(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExternalAndScopedSources builds an index over one local (project) source
// and two external sources (an HTTPS doc scoped to organization, and an insecure
// http URL that must be rejected without aborting the build).
func TestExternalAndScopedSources(t *testing.T) {
	gw := mockGateway(t)
	defer gw.Close()

	doc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><h1>alpha</h1><script>ignore()</script><p>the alpha protocol</p></body></html>"))
	}))
	defer doc.Close()

	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "local.md"), []byte("beta local note"), 0o644)

	idx := New(Config{
		Sources: []SourceSpec{
			{Kind: KindLocal, Root: root, Scope: ScopeProject},
			{Kind: KindExternal, URL: doc.URL, Scope: "org", Name: "AlphaDoc"},
			{Kind: KindExternal, URL: "http://insecure.example/x", Scope: ScopeManual},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	idx.http = doc.Client() // trust the test TLS cert; plain-http embed still works
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	st := idx.Status()
	var ext, loc, insecure *Source
	for k := range st.Sources {
		s := &st.Sources[k]
		switch {
		case s.URL == doc.URL:
			ext = s
		case s.Kind == KindLocal:
			loc = s
		case s.Scope == ScopeManual:
			insecure = s
		}
	}
	if loc == nil || loc.Scope != ScopeProject || loc.Kind != KindLocal {
		t.Errorf("local source wrong: %+v", loc)
	}
	if ext == nil {
		t.Fatal("external source missing")
	}
	if ext.Scope != ScopeOrganization {
		t.Errorf("external scope = %q, want organization", ext.Scope)
	}
	if ext.Path != "AlphaDoc" || ext.Chunks < 1 {
		t.Errorf("external source wrong: %+v", ext)
	}
	if insecure == nil || insecure.Error == "" {
		t.Errorf("insecure http external should record an error: %+v", insecure)
	}

	res, err := idx.Search(context.Background(), "alpha protocol", 1, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Source != doc.URL {
		t.Errorf("top result = %+v, want the external doc", res)
	}
}

func TestBuildAndSearch(t *testing.T) {
	gw := mockGateway(t)
	defer gw.Close()

	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("the alpha protocol is documented here"), 0o644)
	os.WriteFile(filepath.Join(root, "b.md"), []byte("unrelated beta content"), 0o644)
	os.WriteFile(filepath.Join(root, "ignore.bin"), []byte("binary"), 0o644) // non-text ext, skipped

	idx := New(Config{Root: root, Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m"})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if st := idx.Status(); st.Chunks != 2 {
		t.Fatalf("chunks = %d, want 2 (bin skipped)", st.Chunks)
	}

	res, err := idx.Search(context.Background(), "tell me about alpha", 1, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Source != "a.md" {
		t.Fatalf("top result = %+v, want a.md", res)
	}
	if res[0].Score < 0.9 {
		t.Errorf("top score = %v, want high", res[0].Score)
	}
}

// A never-built index must not emit "sources":null — the settings panel reads
// .length off it and a null crashes the render.
func TestStatusEmitsEmptySourcesArray(t *testing.T) {
	b, err := json.Marshal(New(Config{}).Status())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"sources":[]`) {
		t.Fatalf("status JSON = %s, want empty sources array", b)
	}
}
