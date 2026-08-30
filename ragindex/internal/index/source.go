package index

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Following a search result back to the thing it came from.
//
// A search answers with chunks and says which source each came from. That is a
// pointer, and until now there was nothing to dereference it with: an agent
// could be told "the character sheet is at kon/images/dog_sitting.JPEG" and had
// no way to reach it, because the knowledge folder is mounted here and nowhere
// else. For an image that gap is total — images are indexed as metadata, so the
// pointer was the ONLY thing retrieval could ever give back.
//
// This is the other half. The same permission decision as a search, applied to
// a whole source instead of a chunk:
//
//	SourceText   the extracted text, whole and in order — what a chunk is a
//	             slice of, for when five chunks are not the document.
//	SourceBytes  the file itself, for when the point is the bytes.
//
// Authorization is NOT relaxed for being addressed by name. A caller that
// cannot reach a source through a search cannot reach it here either, and it is
// told the same thing in both cases — that the source is not available to it,
// not that it exists and is denied. Anything else would turn this into an
// oracle for what the index holds beyond a run's scope.

// maxSourceBytes bounds one fetch. It matches the agent's own upload ceiling,
// since the point of fetching bytes is usually to send them somewhere.
const maxSourceBytes = 32 << 20

// ErrSourceNotAvailable is returned for a source that is missing, or that the
// caller may not reach. Deliberately one error for both: distinguishing them
// would let a scoped caller enumerate what it is not entitled to.
var ErrSourceNotAvailable = errors.New("no such source, or not available to this caller")

// ErrNoBytes is returned when a source has no file to serve — an external
// document, whose bytes were fetched at ingest and never stored.
var ErrNoBytes = errors.New("this source has no stored file; ask for its text instead")

// findSourceLocked resolves a name to a source the caller may reach, or
// ErrSourceNotAvailable. Names are matched the way membership matches them: the
// stored path or URL, and for an external source its display label too.
func (i *Index) findSourceLocked(name string, f *GroupFilter) (Source, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Source{}, ErrSourceNotAvailable
	}
	for _, s := range i.sources {
		if s.Path != name && sourceKey(s) != name {
			continue
		}
		// The same rule as a chunk: global-and-unclaimed is everyone's, anything
		// else has to match a granted group.
		if !f.permits(s.Groups, s.Scope == ScopeGlobal) {
			return Source{}, ErrSourceNotAvailable
		}
		return s, nil
	}
	return Source{}, ErrSourceNotAvailable
}

// SourceText returns a source's extracted text, whole.
//
// Assembled from the chunks rather than re-read and re-reduced: they are the
// same text, already in order, and reading the file again would let what is
// returned drift from what was searched. For an image or a video the chunks
// hold the metadata descriptor, which is honest — that IS everything the index
// has of it, and saying so is better than an empty answer.
func (i *Index) SourceText(name string, f *GroupFilter) (string, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	src, err := i.findSourceLocked(name, f)
	if err != nil {
		return "", err
	}
	key := sourceKey(src)
	var b strings.Builder
	for _, c := range i.chunks {
		if c.Source == key {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

// SourceBytes returns the file behind a source, with its media class.
//
// Read from disk on demand rather than held in memory: the index keeps text and
// vectors, and keeping every original alongside them would multiply what this
// container holds by the size of the knowledge base for a path most searches
// never take.
func (i *Index) SourceBytes(name string, f *GroupFilter) ([]byte, string, error) {
	i.mu.RLock()
	src, err := i.findSourceLocked(name, f)
	i.mu.RUnlock()
	if err != nil {
		return nil, "", err
	}
	if src.abs == "" {
		return nil, "", ErrNoBytes
	}
	info, err := os.Stat(src.abs)
	if err != nil {
		// The mount is read-only and the file was there at build time, so this
		// means it has since been removed. Not a permission answer — the caller
		// was entitled to it — so it says what actually happened.
		return nil, "", fmt.Errorf("source is no longer on disk: %v", err)
	}
	if info.Size() > maxSourceBytes {
		return nil, "", fmt.Errorf("source is %s, over the %s limit for one fetch", byteSize(info.Size()), byteSize(int64(maxSourceBytes)))
	}
	b, err := os.ReadFile(src.abs)
	if err != nil {
		return nil, "", fmt.Errorf("reading source: %v", err)
	}
	return b, src.Media, nil
}

// byteSize renders a size the way an error message wants it.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
