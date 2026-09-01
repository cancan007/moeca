package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Two registered folders can hold the same relative path. Before their files
// were addressed under the reference they came from, that made them one source:
// one node on the assign screen, and one group membership shared between them —
// so granting a team its own README granted it somebody else's.

func twoFolders(t *testing.T, name string) (string, string) {
	t.Helper()
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(a, name), []byte("ALPHA from folder A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, name), []byte("BETA from folder B"), 0o644); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func qualifiedIndex(t *testing.T, a, b string) *Index {
	t.Helper()
	idx := New(Config{
		Sources: []SourceSpec{
			{Kind: KindLocal, Root: a, Name: "team-a", ID: "team-a-1111"},
			{Kind: KindLocal, Root: b, Name: "team-b", ID: "team-b-2222"},
		},
		EmbedMode: EmbedModeOffline,
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestSameNameInTwoFoldersStaysTwoSources(t *testing.T) {
	a, b := twoFolders(t, "README.md")
	idx := qualifiedIndex(t, a, b)

	paths := map[string]string{}
	for _, s := range idx.Status().Sources {
		paths[s.Path] = s.Origin
		if s.Rel != "README.md" {
			t.Errorf("rel = %q, want the path within its folder", s.Rel)
		}
	}
	if len(paths) != 2 {
		t.Fatalf("sources = %v, want two distinct paths", paths)
	}
	if paths["team-a-1111/README.md"] != "team-a" || paths["team-b-2222/README.md"] != "team-b" {
		t.Errorf("paths = %v", paths)
	}

	// And two nodes on the graph the assign screen draws, which is where they
	// used to collapse into one.
	nodes := idx.Graph().Nodes
	if len(nodes) != 2 {
		t.Fatalf("graph nodes = %d, want 2", len(nodes))
	}
}

// The reason this matters: a grant must land on the file it was made against.
func TestGrantingOneDoesNotGrantTheOther(t *testing.T) {
	a, b := twoFolders(t, "README.md")
	idx := qualifiedIndex(t, a, b)
	// Both are assigned, to different groups. Assigning both is what makes the
	// end-to-end check mean something: an unassigned source is everyone's by
	// design, so leaving one out would test the global default rather than the
	// separation this is about.
	idx.SetGroups(map[string][]string{
		"team-a-1111/README.md": {"team-a-only"},
		"team-b-2222/README.md": {"team-b-only"},
	})

	for _, s := range idx.Status().Sources {
		switch s.Path {
		case "team-a-1111/README.md":
			if len(s.Groups) != 1 || s.Groups[0] != "team-a-only" {
				t.Errorf("the granted file has groups %v", s.Groups)
			}
		case "team-b-2222/README.md":
			if len(s.Groups) != 1 || s.Groups[0] != "team-b-only" {
				t.Errorf("a grant landed on the wrong folder's file: %v", s.Groups)
			}
		}
	}

	// End to end: the run that holds the grant reaches one and not the other.
	res, err := idx.Search(context.Background(), "folder", 10, NewGroupFilter([]string{"team-a-only"}))
	if err != nil {
		t.Fatal(err)
	}
	got := sourcesOf(res)
	if !got["team-a-1111/README.md"] {
		t.Error("the granted file is not reachable")
	}
	if got["team-b-2222/README.md"] {
		t.Error("the other folder's file leaked to a run granted only team-a-only")
	}
}

// Assignments made before paths were qualified name the file by its path within
// the folder alone. They must keep working: an upgrade that silently untagged
// every source would turn each one global — everyone's — which is the wrong
// direction to be wrong in.
func TestLegacyBareAssignmentsStillMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handbook.md"), []byte("alpha handbook"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := New(Config{
		Sources:   []SourceSpec{{Kind: KindLocal, Root: dir, Name: "docs", ID: "docs-9999"}},
		EmbedMode: EmbedModeOffline,
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The name a pre-upgrade graph would have stored.
	if n := idx.SetGroups(map[string][]string{"handbook.md": {"team-a"}}); n != 1 {
		t.Fatalf("matched %d sources by the legacy name, want 1", n)
	}
	src := idx.Status().Sources[0]
	if len(src.Groups) != 1 || src.Groups[0] != "team-a" {
		t.Errorf("groups = %v, want the legacy assignment honoured", src.Groups)
	}
}

// A bare name that now designates two files is refused rather than resolved.
// Picking one would grant a file nobody chose, and the ambiguity resolves itself
// the next time the assignment is saved from the screen.
func TestAnAmbiguousLegacyNameGrantsNothing(t *testing.T) {
	a, b := twoFolders(t, "README.md")
	idx := qualifiedIndex(t, a, b)
	idx.SetGroups(map[string][]string{"README.md": {"team-a-only"}})

	for _, s := range idx.Status().Sources {
		if len(s.Groups) != 0 {
			t.Errorf("%s was granted %v from an ambiguous legacy name", s.Path, s.Groups)
		}
	}
}

// An external document is already unique by its URL and is not qualified.
func TestExternalSourcesAreNotQualified(t *testing.T) {
	idx := New(Config{})
	idx.mu.Lock()
	idx.sources = []Source{{Path: "handbook", Rel: "handbook", URL: "https://example.com/h.md", Kind: KindExternal}}
	idx.mu.Unlock()
	if got := sourceKey(idx.sources[0]); got != "https://example.com/h.md" {
		t.Errorf("sourceKey = %q", got)
	}
}

// The graph must carry Origin AND Rel, not just Origin.
//
// Rel is how a name stored before sources were qualified is recognised: without
// it the screen cannot tell that "kon/images/dog.JPEG" and
// "moeca_rag-881f/kon/images/dog.JPEG" are one file, shows an assigned source as
// unassigned, and stores it a second time when someone ticks it. That is what
// happened, and it happened because this field was added to Source and not
// copied onto the node.
func TestGraphNodesCarryOriginAndRel(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "kon", "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kon", "images", "dog.md"), []byte("alpha dog"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := New(Config{
		Sources:   []SourceSpec{{Kind: KindLocal, Root: dir, Name: "moeca_rag", ID: "moeca_rag-881f"}},
		EmbedMode: EmbedModeOffline,
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	nodes := idx.Graph().Nodes
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d", len(nodes))
	}
	n := nodes[0]
	if n.Source != "moeca_rag-881f/kon/images/dog.md" {
		t.Errorf("source = %q", n.Source)
	}
	if n.Origin != "moeca_rag" {
		t.Errorf("origin = %q", n.Origin)
	}
	if n.Rel != "kon/images/dog.md" {
		t.Errorf("rel = %q — without it a legacy assignment cannot be recognised", n.Rel)
	}
}
