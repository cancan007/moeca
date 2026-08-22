package index

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The index is derived state and can always be rebuilt; what is worth keeping
// across a restart is the part that cost money. These pin that the vectors
// survive, and that they are refused whenever they might not mean the same
// thing any more.

func TestVectorsSurviveARestart(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("alpha content"), 0o644)

	first := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, CacheDir: cache, EmbedModel: "m"})
	if err := first.Build(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A new process over the same directory: nothing in memory, everything on
	// disk.
	second := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, CacheDir: cache, EmbedModel: "m"})
	calls := 0
	second.embedHook = func(n int) { calls += n }
	second.LoadCache()
	if err := second.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("a restart re-embedded %d chunks, want 0", calls)
	}
	if st := second.Status(); st.Reused == 0 {
		t.Errorf("status reported nothing reused: %+v", st)
	}
}

// Vectors are only comparable to others from the same model. Mixing two
// embedding spaces would degrade retrieval in a way nothing on screen explains.
func TestACacheFromAnotherModelIsDiscarded(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("alpha content"), 0o644)

	first := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, CacheDir: cache, EmbedModel: "small"})
	first.Build(context.Background())

	second := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, CacheDir: cache, EmbedModel: "large"})
	calls := 0
	second.embedHook = func(n int) { calls += n }
	second.LoadCache()
	second.Build(context.Background())
	if calls == 0 {
		t.Error("vectors from another model were reused")
	}
}

// Offline vectors are a local approximation, not the model's. A cache built in
// one mode must not be handed to the other.
func TestACacheFromAnotherEmbedModeIsDiscarded(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("alpha content"), 0o644)

	first := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, CacheDir: cache, EmbedModel: "m"})
	first.Build(context.Background())

	second := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, CacheDir: cache, EmbedModel: "m"})
	second.LoadCache()
	second.mu.RLock()
	n := len(second.vecCache)
	second.mu.RUnlock()
	if n != 0 {
		t.Errorf("loaded %d vectors built in the other embed mode", n)
	}
}

// A damaged file costs a re-embed, not a crash and not a wrong answer.
func TestADamagedCacheIsIgnored(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("alpha"), 0o644)
	os.WriteFile(filepath.Join(cache, vecCacheFile), []byte("garbage, not a cache"), 0o644)

	idx := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, CacheDir: cache, EmbedModel: "m"})
	idx.LoadCache()
	calls := 0
	idx.embedHook = func(n int) { calls += n }
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Error("a damaged cache was trusted")
	}
}

// With nowhere to write, everything behaves as it did before persistence
// existed: vectors live in memory for the life of the process.
func TestNoCacheDirWritesNothing(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.md"), []byte("alpha"), 0o644)
	idx := New(Config{Sources: []SourceSpec{{Kind: KindLocal, Root: root}}, EmbedMode: EmbedModeOffline, EmbedModel: "m"})
	idx.LoadCache()
	if err := idx.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestVecCacheRoundTrip(t *testing.T) {
	in := map[string][]float32{
		string(make([]byte, 32)):            {0.5, -0.25, 1},
		string(bytes.Repeat([]byte{7}, 32)): {1, 2, 3},
	}
	var buf bytes.Buffer
	if err := writeVecCache(&buf, in, "model-x", "gateway"); err != nil {
		t.Fatal(err)
	}
	out, model, mode, err := readVecCache(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if model != "model-x" || mode != "gateway" {
		t.Errorf("header = %q/%q", model, mode)
	}
	if len(out) != len(in) {
		t.Fatalf("read %d entries, want %d", len(out), len(in))
	}
	for k, want := range in {
		got := out[k]
		if len(got) != len(want) {
			t.Fatalf("entry width %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("entry %x[%d] = %v, want %v", k, i, got[i], want[i])
			}
		}
	}
}
