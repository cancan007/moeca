package index

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// Keeping the vectors across a restart.
//
// The index itself is derived state — sources are the truth, and rebuilding
// from them is always correct. What is worth keeping is the part that cost
// money and time: the embedding of a chunk that has not changed. So the vectors
// are what persists, not the index, and a restarted indexer still re-reads and
// re-chunks every source (cheap, local) while paying the provider only for text
// it has never seen.
//
// That choice also removes the staleness question. A cache keyed by the text's
// own hash cannot disagree with the files: an edit made while the indexer was
// down produces a chunk that simply is not in the cache, and gets embedded.
// Persisting the assembled index instead would need a validity check against
// every source, which is the same work with a way to be wrong.
//
// Vectors are only comparable to others from the same model, so the file states
// which one produced them and is discarded whole if that no longer matches.
// Silently mixing two embedding spaces would degrade retrieval in a way nothing
// on screen would explain.

const (
	vecCacheMagic   = "MOECAVEC"
	vecCacheVersion = 1
	// vecCacheFile is the name inside the configured cache directory.
	vecCacheFile = "vectors.bin"
)

// vecCachePath returns where the cache lives, or "" when none is configured —
// in which case the vectors stay in memory, as they always did.
func (i *Index) vecCachePath() string {
	if i.cfg.CacheDir == "" {
		return ""
	}
	return filepath.Join(i.cfg.CacheDir, vecCacheFile)
}

// LoadCache reads previously embedded vectors, if any are on disk and they were
// produced by the configuration now in force. A missing, damaged or mismatched
// file is not an error: it means the next build pays for everything, which is
// exactly what happened before this existed.
func (i *Index) LoadCache() {
	path := i.vecCachePath()
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return // no cache yet
	}
	defer f.Close()

	cache, model, mode, err := readVecCache(bufio.NewReader(f))
	if err != nil {
		log.Printf("ragindex: ignoring vector cache: %v", err)
		return
	}
	if model != i.cfg.EmbedModel || mode != i.embedModeName() {
		log.Printf("ragindex: vector cache was built by %s/%s, now %s/%s — discarding it",
			model, mode, i.cfg.EmbedModel, i.embedModeName())
		return
	}
	i.mu.Lock()
	i.vecCache = cache
	i.mu.Unlock()
	log.Printf("ragindex: loaded %d cached vectors from %s", len(cache), path)
}

// saveCache writes the vectors for the build that just finished. Best effort:
// losing it costs a re-embed, which is not worth failing a good build over.
func (i *Index) saveCache(cache map[string][]float32) {
	path := i.vecCachePath()
	if path == "" || len(cache) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("ragindex: vector cache dir: %v", err)
		return
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		log.Printf("ragindex: writing vector cache: %v", err)
		return
	}
	w := bufio.NewWriter(f)
	err = writeVecCache(w, cache, i.cfg.EmbedModel, i.embedModeName())
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Printf("ragindex: writing vector cache: %v", err)
		_ = os.Remove(tmp)
		return
	}
	// Renamed into place so a reader never sees a half-written file — the same
	// reason the index itself is swapped rather than mutated.
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("ragindex: publishing vector cache: %v", err)
		_ = os.Remove(tmp)
	}
}

func (i *Index) embedModeName() string {
	if i.cfg.EmbedMode == "" {
		return EmbedModeGateway
	}
	return i.cfg.EmbedMode
}

// The file is a header naming what produced the vectors, then fixed-width
// entries. Explicit rather than a general encoder because the shape is a hash
// and a float array, and 6 KB per chunk is worth being exact about.
func writeVecCache(w io.Writer, cache map[string][]float32, model, mode string) error {
	dim := 0
	for _, v := range cache {
		dim = len(v)
		break
	}
	if dim == 0 {
		return fmt.Errorf("no vectors to write")
	}
	if _, err := io.WriteString(w, vecCacheMagic); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint8(vecCacheVersion)); err != nil {
		return err
	}
	for _, s := range []string{model, mode} {
		if err := binary.Write(w, binary.LittleEndian, uint16(len(s))); err != nil {
			return err
		}
		if _, err := io.WriteString(w, s); err != nil {
			return err
		}
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(dim)); err != nil {
		return err
	}
	// Entries of a different width belong to another model and cannot be mixed
	// in; they are dropped rather than written alongside.
	var count uint32
	for _, v := range cache {
		if len(v) == dim {
			count++
		}
	}
	if err := binary.Write(w, binary.LittleEndian, count); err != nil {
		return err
	}
	for key, vec := range cache {
		if len(vec) != dim || len(key) != 32 {
			continue
		}
		if _, err := io.WriteString(w, key); err != nil {
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, vec); err != nil {
			return err
		}
	}
	return nil
}

func readVecCache(r io.Reader) (map[string][]float32, string, string, error) {
	magic := make([]byte, len(vecCacheMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != vecCacheMagic {
		return nil, "", "", fmt.Errorf("not a vector cache")
	}
	var version uint8
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, "", "", err
	}
	if version != vecCacheVersion {
		return nil, "", "", fmt.Errorf("version %d, this build writes %d", version, vecCacheVersion)
	}
	strs := make([]string, 2)
	for k := range strs {
		var n uint16
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return nil, "", "", err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, "", "", err
		}
		strs[k] = string(b)
	}
	var dim, count uint32
	if err := binary.Read(r, binary.LittleEndian, &dim); err != nil {
		return nil, "", "", err
	}
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return nil, "", "", err
	}
	if dim == 0 || dim > 1<<16 {
		return nil, "", "", fmt.Errorf("implausible vector width %d", dim)
	}
	cache := make(map[string][]float32, count)
	key := make([]byte, 32)
	for n := uint32(0); n < count; n++ {
		if _, err := io.ReadFull(r, key); err != nil {
			return nil, "", "", fmt.Errorf("entry %d: %w", n, err)
		}
		vec := make([]float32, dim)
		if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
			return nil, "", "", fmt.Errorf("entry %d: %w", n, err)
		}
		cache[string(key)] = vec
	}
	return cache, strs[0], strs[1], nil
}
