package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Following a search result back to its source must not be a way around the
// filter that decided which results the caller saw in the first place.

func fetchableIndex(t *testing.T) *Index {
	t.Helper()
	gw := mockGateway(t)
	t.Cleanup(gw.Close)
	mk := func(name, body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	idx := New(Config{
		Sources: []SourceSpec{
			{Kind: KindLocal, Root: mk("handbook.md", "alpha handbook body")},
			{Kind: KindLocal, Root: mk("payroll.md", "alpha payroll body")},
		},
		Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m",
	})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	idx.SetGroups(map[string][]string{"payroll.md": {"finance"}})
	return idx
}

// The point of the whole feature: a source a search reached can be read whole.
func TestSourceTextReturnsTheWholeDocument(t *testing.T) {
	idx := fetchableIndex(t)
	text, err := idx.SourceText("payroll.md", NewGroupFilter([]string{"finance"}))
	if err != nil {
		t.Fatalf("SourceText: %v", err)
	}
	if !strings.Contains(text, "alpha payroll body") {
		t.Errorf("text = %q, want the document body", text)
	}
}

// And the point of doing it here rather than by mounting anything: naming a
// source is not a way to reach one the caller was not granted.
func TestSourceIsRefusedToACallerThatCannotSearchIt(t *testing.T) {
	idx := fetchableIndex(t)
	f := NewGroupFilter([]string{"team-a"})

	if _, err := idx.SourceText("payroll.md", f); !errors.Is(err, ErrSourceNotAvailable) {
		t.Errorf("SourceText on an ungranted source = %v, want ErrSourceNotAvailable", err)
	}
	if _, _, err := idx.SourceBytes("payroll.md", f); !errors.Is(err, ErrSourceNotAvailable) {
		t.Errorf("SourceBytes on an ungranted source = %v, want ErrSourceNotAvailable", err)
	}
	// The source nobody claimed is still everyone's, by the same rule searches use.
	if _, err := idx.SourceText("handbook.md", f); err != nil {
		t.Errorf("an unassigned source must stay readable: %v", err)
	}
}

// Missing and forbidden must be one answer, or a scoped caller could enumerate
// what it is not entitled to one name at a time.
func TestAMissingSourceIsIndistinguishableFromAForbiddenOne(t *testing.T) {
	idx := fetchableIndex(t)
	f := NewGroupFilter([]string{"team-a"})
	_, missing := idx.SourceText("nothing-here.md", f)
	_, forbidden := idx.SourceText("payroll.md", f)
	if !errors.Is(missing, ErrSourceNotAvailable) || !errors.Is(forbidden, ErrSourceNotAvailable) {
		t.Fatalf("missing = %v, forbidden = %v", missing, forbidden)
	}
	if missing.Error() != forbidden.Error() {
		t.Errorf("the two answers differ:\n missing:   %v\n forbidden: %v", missing, forbidden)
	}
}

// An unscoped caller carries no policy and reads what it always could.
func TestAnUnscopedCallerReadsAnySource(t *testing.T) {
	idx := fetchableIndex(t)
	if _, err := idx.SourceText("payroll.md", nil); err != nil {
		t.Errorf("nil filter states no policy and must permit: %v", err)
	}
}

// The bytes are the whole reason this exists: an image's contents are not in
// the index at all, so the file is the only way to get them.
func TestSourceBytesReturnsTheFile(t *testing.T) {
	idx := fetchableIndex(t)
	b, media, err := idx.SourceBytes("handbook.md", NewGroupFilter([]string{"team-a"}))
	if err != nil {
		t.Fatalf("SourceBytes: %v", err)
	}
	if string(b) != "alpha handbook body" {
		t.Errorf("bytes = %q", b)
	}
	if media == "" {
		t.Error("the media class should come back with the bytes")
	}
}

// An external document was fetched at ingest and never stored, so there is no
// file to serve. Said plainly, and distinctly from a permission answer — the
// caller was entitled to it and asking differently will work.
func TestAnExternalSourceHasNoBytes(t *testing.T) {
	idx := New(Config{})
	idx.mu.Lock()
	idx.sources = []Source{{Path: "handbook", URL: "https://example.com/handbook.md", Kind: KindExternal, Scope: ScopeGlobal}}
	idx.mu.Unlock()

	if _, _, err := idx.SourceBytes("https://example.com/handbook.md", nil); !errors.Is(err, ErrNoBytes) {
		t.Errorf("SourceBytes on an external source = %v, want ErrNoBytes", err)
	}
	// Addressable by its display label too, the way membership accepts both.
	if _, _, err := idx.SourceBytes("handbook", nil); !errors.Is(err, ErrNoBytes) {
		t.Errorf("external source not found by its label: %v", err)
	}
}
