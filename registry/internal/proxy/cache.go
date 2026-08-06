package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The artifact cache.
//
// Sandboxes are disposable: their toolchain caches live on tmpfs and die with
// the container, so without a cache every run re-downloads its whole dependency
// tree. The durable cache therefore lives HERE, in the proxy, rather than in a
// volume shared between sandboxes — a shared writable cache mount would let one
// task plant bytes that another task later executes, which is exactly the
// cross-task contamination the worktree-per-task rule exists to prevent. Here
// the sandbox can only *read* the cache, over HTTP, through this proxy.
//
// Only immutable artifacts are cached (see Ecosystem.Immutable): a published
// tarball / wheel / module zip never changes its bytes, so a hit is always
// correct. Mutable metadata (version listings) is never cached.

// Cache is a size-bounded, content-addressed store of immutable artifacts.
// It is safe for concurrent use.
type Cache struct {
	dir string
	max int64

	mu    sync.Mutex
	bytes int64 // total size of stored entries, maintained across put/evict
}

// NewCache opens (creating if needed) a cache rooted at dir and bounded to max
// bytes. A max of 0 or a dir that cannot be created disables caching — the proxy
// still works, it just fetches upstream every time.
func NewCache(dir string, max int64) *Cache {
	if dir == "" || max <= 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	c := &Cache{dir: dir, max: max}
	c.bytes = c.scanSize()
	return c
}

// cacheKey derives the filesystem-safe key for one ecosystem+path pair. The
// ecosystem is part of the key so two registries can never alias each other.
func cacheKey(ecosystem, path string) string {
	sum := sha256.Sum256([]byte(ecosystem + "\x00" + path))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) bodyPath(key string) string { return filepath.Join(c.dir, key+".bin") }
func (c *Cache) metaPath(key string) string { return filepath.Join(c.dir, key+".type") }

// scanSize totals the bytes currently stored (called once, at open).
func (c *Cache) scanSize() int64 {
	var total int64
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// Path returns the on-disk body path for a key, or "" when caching is off.
func (c *Cache) Path(key string) string {
	if c == nil {
		return ""
	}
	return c.bodyPath(key)
}

// Get returns the cached body path and content type for a key. A miss (or a
// nil cache) is (_, _, false).
func (c *Cache) Get(key string) (path, contentType string, ok bool) {
	if c == nil {
		return "", "", false
	}
	body := c.bodyPath(key)
	st, err := os.Stat(body)
	if err != nil || st.IsDir() {
		return "", "", false
	}
	ct, _ := os.ReadFile(c.metaPath(key))
	// Touch so eviction (oldest-first) approximates least-recently-used rather
	// than least-recently-written.
	now := time.Now()
	_ = os.Chtimes(body, now, now)
	return body, string(ct), true
}

// Put commits an already-written temp file into the cache under key. The temp
// file is renamed (never copied), so a partially-downloaded file can never be
// observed as a hit. On any error the entry is simply not cached.
func (c *Cache) Put(key, contentType, tmpPath string, size int64) {
	if c == nil {
		return
	}
	if size > c.max {
		_ = os.Remove(tmpPath) // one entry may not exceed the whole budget
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Rename(tmpPath, c.bodyPath(key)); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	_ = os.WriteFile(c.metaPath(key), []byte(contentType), 0o600)
	c.bytes += size + int64(len(contentType))
	c.evictLocked()
}

// evictLocked removes the oldest entries until the cache is back under budget.
func (c *Cache) evictLocked() {
	if c.bytes <= c.max {
		return
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type item struct {
		key  string
		mod  int64
		size int64
	}
	var items []item
	sizes := map[string]int64{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		sizes[name] = info.Size()
		if filepath.Ext(name) != ".bin" {
			continue
		}
		items = append(items, item{key: name[:len(name)-len(".bin")], mod: info.ModTime().UnixNano(), size: info.Size()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod < items[j].mod })
	for _, it := range items {
		if c.bytes <= c.max {
			return
		}
		freed := it.size + sizes[it.key+".type"]
		if err := os.Remove(c.bodyPath(it.key)); err != nil {
			continue
		}
		_ = os.Remove(c.metaPath(it.key))
		c.bytes -= freed
		if c.bytes < 0 {
			c.bytes = 0
		}
	}
}
