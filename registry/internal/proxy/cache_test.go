package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// put writes size bytes into the cache under key, via the same temp-file +
// rename path the proxy uses.
func put(t *testing.T, c *Cache, key string, size int) {
	t.Helper()
	f, err := os.CreateTemp(c.dir, "test-*")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write([]byte(strings.Repeat("x", size))); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	c.Put(key, "application/octet-stream", f.Name(), int64(size))
}

func TestCacheRoundTrip(t *testing.T) {
	c := NewCache(t.TempDir(), 1<<20)
	if c == nil {
		t.Fatal("NewCache returned nil for a valid dir")
	}
	put(t, c, "k1", 100)

	path, ctype, ok := c.Get("k1")
	if !ok {
		t.Fatal("Get(k1) missed after Put")
	}
	if ctype != "application/octet-stream" {
		t.Errorf("content type = %q", ctype)
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) != 100 {
		t.Errorf("body = %d bytes, err = %v; want 100 bytes", len(b), err)
	}
	if _, _, ok := c.Get("missing"); ok {
		t.Error("Get(missing) reported a hit")
	}
}

// The cache is a bounded disk budget, not a leak. Once it is over budget the
// oldest entries go first.
func TestCacheEvictsOldestWhenOverBudget(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, 250)

	put(t, c, "old", 100)
	// Age the first entry so eviction order is unambiguous.
	old := filepath.Join(dir, "old.bin")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	put(t, c, "new", 100)
	if _, _, ok := c.Get("old"); !ok {
		t.Fatal("old evicted before the budget was exceeded")
	}
	// Re-age: Get touches the entry (LRU), which would otherwise make "old" the
	// newest and invert the eviction order.
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	put(t, c, "newest", 100) // now over the 250-byte budget

	if _, _, ok := c.Get("old"); ok {
		t.Error("oldest entry survived eviction")
	}
	if _, _, ok := c.Get("newest"); !ok {
		t.Error("the entry just written was evicted")
	}
}

// An artifact larger than the entire budget must not evict everything else to
// make room for itself.
func TestCacheRefusesAnEntryLargerThanTheBudget(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, 200)
	put(t, c, "small", 50)
	put(t, c, "huge", 500)

	if _, _, ok := c.Get("huge"); ok {
		t.Error("an over-budget entry was cached")
	}
	if _, _, ok := c.Get("small"); !ok {
		t.Error("an over-budget entry evicted a valid one")
	}
}

// Caching disabled must degrade to "always fetch upstream", never to a panic.
func TestNilCacheIsInert(t *testing.T) {
	var c *Cache
	if _, _, ok := c.Get("k"); ok {
		t.Error("nil cache reported a hit")
	}
	c.Put("k", "text/plain", filepath.Join(t.TempDir(), "nope"), 1)
	if got := c.Path("k"); got != "" {
		t.Errorf("nil cache Path = %q, want \"\"", got)
	}
	if NewCache("", 1<<20) != nil {
		t.Error("NewCache with no dir should disable caching")
	}
	if NewCache(t.TempDir(), 0) != nil {
		t.Error("NewCache with a zero budget should disable caching")
	}
}

func TestCacheKeysAreNamespacedByEcosystem(t *testing.T) {
	if cacheKey("npm", "/a") == cacheKey("pypi", "/a") {
		t.Error("two ecosystems collide on the same path")
	}
	if cacheKey("npm", "/a") != cacheKey("npm", "/a") {
		t.Error("cacheKey is not deterministic")
	}
}
