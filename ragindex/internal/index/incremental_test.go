package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A rebuild reads and re-chunks everything, which is cheap. What costs is the
// embedding call, so that is what an unchanged file must stop paying for.

// countingIndex builds over root and records how many texts were embedded.
func countingIndex(t *testing.T, root string) (*Index, *int) {
	t.Helper()
	calls := 0
	idx := New(Config{
		Sources:   []SourceSpec{{Kind: KindLocal, Root: root, Scope: ScopeGlobal}},
		EmbedMode: EmbedModeOffline,
	})
	idx.embedHook = func(n int) { calls += n }
	return idx, &calls
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestARebuildEmbedsNothingWhenNothingChanged(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "alpha content")
	write(t, root, "b.md", "beta content")
	idx, calls := countingIndex(t, root)

	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := *calls
	if first == 0 {
		t.Fatal("the first build embedded nothing")
	}

	*calls = 0
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("a rebuild with no changes embedded %d chunks, want 0", *calls)
	}
	if st := idx.Status(); st.Reused != first || st.Embedded != 0 {
		t.Errorf("status reported embedded=%d reused=%d, want 0/%d", st.Embedded, st.Reused, first)
	}
}

// The point of the feature: a new file costs its own chunks and nothing else.
func TestOnlyANewFileIsEmbedded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "alpha content")
	idx, calls := countingIndex(t, root)
	idx.Build(context.Background())

	*calls = 0
	write(t, root, "c.md", "gamma content")
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("adding one file embedded %d chunks, want 1", *calls)
	}
}

func TestOnlyAChangedFileIsEmbedded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "alpha content")
	write(t, root, "b.md", "beta content")
	idx, calls := countingIndex(t, root)
	idx.Build(context.Background())

	*calls = 0
	write(t, root, "b.md", "beta content, rewritten")
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("rewriting one file embedded %d chunks, want 1", *calls)
	}
}

// Touching a file without altering it is not a change. A timestamp would say
// otherwise; the text does not.
func TestATouchedButUnalteredFileIsNotReEmbedded(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "alpha content")
	idx, calls := countingIndex(t, root)
	idx.Build(context.Background())

	*calls = 0
	write(t, root, "a.md", "alpha content") // same bytes, new mtime
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Errorf("a touched file embedded %d chunks, want 0", *calls)
	}
}

// A deleted file's vector is dropped rather than kept for the life of the
// process: the cache is the current build's, not everything ever seen.
func TestADeletedFileLeavesTheCache(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "alpha content")
	write(t, root, "b.md", "beta content")
	idx, _ := countingIndex(t, root)
	idx.Build(context.Background())

	os.Remove(filepath.Join(root, "b.md"))
	idx.Build(context.Background())

	idx.mu.RLock()
	n := len(idx.vecCache)
	idx.mu.RUnlock()
	if n != 1 {
		t.Errorf("cache holds %d entries after a deletion, want 1", n)
	}
}

// Two files with identical text embed once, not twice.
func TestDuplicateTextEmbedsOnce(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", "identical")
	write(t, root, "b.md", "identical")
	idx, calls := countingIndex(t, root)

	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("two identical chunks embedded %d times, want 1", *calls)
	}
	// Both chunks still carry a vector.
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, c := range idx.chunks {
		if len(c.vec) == 0 {
			t.Fatalf("chunk %q has no vector", c.Source)
		}
	}
}
