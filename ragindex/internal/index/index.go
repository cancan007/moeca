// Package index builds an in-memory embedding index over a read-only knowledge
// root and answers similarity queries. Embeddings are obtained THROUGH the
// Orchestra gateway (the same single-egress path agents use), so this service
// holds no API key — the gateway injects it. The knowledge lives only here, on a
// read-only mount; sandboxed agents reach search only via the gateway's /rag
// route, never this service (or the host) directly.
package index

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Chunk is one indexed passage with its source and embedding.
type Chunk struct {
	Source string `json:"source"`
	Text   string `json:"text"`
	vec    []float32
	// groups is the permission label set inherited from the chunk's source.
	// Every chunk of a source shares one slice rather than copying it, so
	// tagging costs a pointer per chunk and not a string set.
	groups []string
	// global marks a chunk from a globally-scoped source, which every search
	// reaches regardless of the groups it was granted. Carried per chunk rather
	// than looked up by source at query time so the filter stays a field read
	// in a linear scan.
	global bool
}

// Config parameterises the indexer.
type Config struct {
	Root         string       // legacy single local knowledge root (read-only mount)
	Sources      []SourceSpec // knowledge sources (local dirs + external HTTPS); overrides Root when set
	Gateway      string       // gateway origin, e.g. http://orchestra-gateway:8787
	Session      string       // X-Orchestra-Session
	EmbedPrefix  string       // gateway route for embeddings, e.g. "/openai"
	EmbedModel   string       // e.g. text-embedding-3-small
	MaxChunkRune int          // chunk size in runes (0 => 1200)
	// EmbedMode selects where vectors come from: the gateway (default) or a
	// local, keyless approximation for demos and tests. See offline.go.
	EmbedMode string
	// CacheDir is a writable directory where embedded vectors are kept across
	// restarts. Empty keeps them in memory only. See veccache.go.
	CacheDir string
	// CaptionModel turns on describing images with a vision model so their
	// contents become searchable. Empty leaves them indexed by path and
	// filename alone, which is the default: captioning costs a model call per
	// picture, and a knowledge base should not start spending on a rebuild
	// because someone registered a folder. See caption.go.
	CaptionModel string
	// CaptionPrefix is the gateway route the vision model is behind. Defaults
	// to EmbedPrefix, since the same provider usually serves both.
	CaptionPrefix string
}

// Source kinds and scopes.
const (
	KindLocal    = "local"
	KindExternal = "external"

	// ScopeGlobal is the default: knowledge every task may retrieve, whatever
	// groups its run was granted — for as long as it belongs to no group.
	// Assigning a source to a group is what narrows it, so the scope a source is
	// actually reachable under is derived rather than declared twice. See
	// permits in groups.go and effectiveScope in membership.go.
	ScopeGlobal       = "global"
	ScopeProject      = "project"
	ScopeOrganization = "organization"
)

// SourceSpec declares one knowledge source to index. A source is either a local
// directory (Kind=local, Root set — a read-only mount) or an external document
// fetched over HTTPS (Kind=external, URL set). Scope tags where the knowledge
// belongs (project / organization / manual) — orthogonal to Kind, so both local
// and external sources can live under either scope.
type SourceSpec struct {
	Kind  string `json:"kind"`  // "local" | "external"
	Root  string `json:"root"`  // local: directory to walk
	URL   string `json:"url"`   // external: https URL to fetch
	Scope string `json:"scope"` // "global" | "project" | "organization"
	Name  string `json:"name"`  // optional display label
	// ID is the stable, unique name this reference's files are addressed under.
	//
	// A local path is relative to whichever root it was found under, so two
	// registered folders both holding "docs/overview.md" would give one
	// identifier to two different files — and the screen that assigns sources to
	// groups would then grant or withhold them as one. Prefixing with the
	// reference's id keeps them apart.
	//
	// Supplied by the shell, which knows the host path and can make it stable:
	// an identifier derived from a position in the list would move when the list
	// was reordered, silently detaching every assignment made against it. Empty
	// falls back to the display name, which is what a hand-written config gets.
	ID string `json:"id,omitempty"`
	// Groups are permission labels; a scoped search sees this source only if it
	// permits one of them. A source with no groups is visible to unscoped
	// searches only — unless its scope is global, in which case being in no
	// group is precisely what keeps it everyone's. See groups.go.
	Groups []string `json:"groups,omitempty"`
}

// Index is the live, mutable in-memory vector store.
type Index struct {
	cfg  Config
	http *http.Client

	// membership holds the host-pushed source→groups mapping, re-applied after
	// every build so a reindex cannot silently drop every permission label.
	membership membership
	// captions holds descriptions taken from a vision model, keyed by the
	// content of the file described, so a rebuild pays only for pictures it has
	// never seen. See caption.go.
	captions captionStore

	mu       sync.RWMutex
	chunks   []Chunk
	sources  []Source
	building bool
	lastErr  string
	builtAt  time.Time
	// vecCache holds the vector for a chunk's text, so a rebuild pays only for
	// what actually changed. See reuseVectors.
	vecCache map[string][]float32
	// embedHook counts texts sent for embedding. Tests only: how much a rebuild
	// re-embeds is the behaviour under test, and it is invisible from outside.
	embedHook func(n int)
	// Counts from the last build, for /status: how much was re-embedded and how
	// much was reused. Without them "the reindex was fast" is a feeling rather
	// than a fact.
	lastEmbedded int
	lastReused   int
}

// Source is a per-source summary for the UI. Kind/Scope let the UI group and
// badge sources (local vs external HTTPS, project vs organization); URL is set
// for external sources; Error carries a per-source fetch failure without failing
// the whole build.
//
// Scope here is the EFFECTIVE scope — what a search will actually treat this
// source as — not necessarily the configured one, because group membership
// narrows it. See effectiveScope in membership.go.
type Source struct {
	Path   string `json:"path"`
	Chunks int    `json:"chunks"`
	Kind   string `json:"kind"`            // "local" | "external"
	Scope  string `json:"scope"`           // "global" | "project" | "organization"
	URL    string `json:"url,omitempty"`   // external sources only
	Error  string `json:"error,omitempty"` // per-source ingestion error, if any
	// Media is what the file is (text / csv / pdf / image / video / subtitle)
	// and Content is what was actually indexed for it (its own text, or only
	// path-and-filename metadata). Both are needed: a screenshot listed with a
	// chunk count next to a Markdown file would read as "searchable contents",
	// which for an image is not true today. See media.go.
	Media   string `json:"media,omitempty"`
	Content string `json:"content,omitempty"`
	// Note carries a non-fatal remark — truncation, a PDF with no text layer,
	// a video with no caption track. Unlike Error it does not mean the source
	// failed; it means the result is narrower than the file.
	Note string `json:"note,omitempty"`
	// Groups this source is labelled with, for the UI to show why a search did
	// or did not reach it.
	Groups []string `json:"groups,omitempty"`
	// Origin is the display name of the registered reference this file came
	// from — the folder an operator added, or the external document itself. For
	// showing; Path already carries the identity.
	Origin string `json:"origin,omitempty"`
	// Rel is the path within that reference, which is what a person recognises.
	// Path is Origin's id joined to this, and is what everything addresses.
	Rel string `json:"rel,omitempty"`
	// declared is the scope the source was CONFIGURED with, kept because Scope
	// above is the EFFECTIVE one and membership can narrow it. Recomputing from
	// the declared value each time is what lets a source widen again when it is
	// taken back out of every group; deriving from the reported field would
	// make the narrowing one-way.
	declared string
	// abs is where a local source's bytes are on disk, kept so they can be
	// served without re-deriving which configured root the file came from.
	// Empty for external sources, whose bytes were never stored.
	abs string
	// assumedGlobal marks a source that is global because nothing said
	// otherwise, as opposed to one whose config says so. The two are the same
	// scope and behave identically once membership is known; they differ only
	// when NOTHING is known, where a declaration is a person's word and a
	// default is nobody's. See closeUnclaimedLocked.
	assumedGlobal bool
}

func New(cfg Config) *Index {
	if cfg.MaxChunkRune <= 0 {
		cfg.MaxChunkRune = 1200
	}
	return &Index{cfg: cfg, http: &http.Client{Timeout: 120 * time.Second}}
}

// File extensions read as plain text/code. Spreadsheets, PDFs, images and
// video are handled by class in media.go — each needs a different reduction to
// text, and one of them (images) has no honest reduction yet.
var textExt = map[string]bool{
	".md": true, ".mdx": true, ".txt": true, ".rst": true, ".go": true, ".py": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".json": true, ".yaml": true,
	".yml": true, ".toml": true, ".html": true, ".css": true, ".sh": true, ".rs": true,
}

// Build ingests every configured source (local directories and external HTTPS
// documents), chunks the text, embeds the chunks through the gateway, and
// atomically swaps in the new index. Per-source ingestion failures are recorded
// on the Source and do not abort the whole build.
func (i *Index) Build(ctx context.Context) error {
	i.mu.Lock()
	if i.building {
		i.mu.Unlock()
		return fmt.Errorf("index build already in progress")
	}
	i.building = true
	i.mu.Unlock()
	defer func() { i.mu.Lock(); i.building = false; i.mu.Unlock() }()

	var chunks []Chunk
	var sources []Source
	for _, spec := range i.effectiveSources() {
		scope := normalizeScope(spec.Scope)
		if spec.Kind == KindExternal {
			src := i.ingestExternal(ctx, spec, scope, &chunks)
			sources = append(sources, src)
			continue
		}
		sources = append(sources, i.ingestLocal(ctx, spec, scope, &chunks)...)
	}
	if len(chunks) == 0 {
		i.swap(nil, sources, "")
		return nil
	}

	// Only what changed is embedded; everything else keeps the vector it
	// already had. See reuseVectors for why the boundary is here.
	pending, dupes, next := i.reuseVectors(chunks)

	const batch = 64
	for start := 0; start < len(pending); start += batch {
		end := start + batch
		if end > len(pending) {
			end = len(pending)
		}
		texts := make([]string, 0, end-start)
		for _, idx := range pending[start:end] {
			texts = append(texts, chunks[idx].Text)
		}
		vecs, err := i.embed(ctx, texts)
		if err != nil {
			i.setErr(err)
			return err
		}
		for k, idx := range pending[start:end] {
			chunks[idx].vec = vecs[k]
			next[textKey(chunks[idx].Text)] = vecs[k]
		}
	}

	// Chunks that repeat a text being embedded in this same build: they waited
	// for the vector rather than queueing a second identical call.
	for _, d := range dupes {
		chunks[d.idx].vec = next[d.key]
	}

	i.mu.Lock()
	i.vecCache = next
	i.lastEmbedded = len(pending)
	i.lastReused = len(chunks) - len(pending)
	i.mu.Unlock()

	// Written after the vectors are complete and only when this build added
	// something: rewriting an unchanged cache would burn the disk for nothing.
	if len(pending) > 0 {
		i.saveCache(next)
	}
	// Captions are saved on every build that could have taken one. Unlike
	// vectors they cost a model call each, so the cheap write is worth doing
	// even when nothing new was described — a caption lost to a restart is
	// paid for a second time.
	if i.captionEnabled() {
		i.saveCaptions()
	}

	i.swap(chunks, sources, "")
	return nil
}

// reuseVectors fills in the chunks whose text this index has already embedded,
// and reports the indices of the ones it has not.
//
// A rebuild reads and re-chunks every source, which is cheap — the cost of an
// index is the embedding calls, and those are what a changed file should be
// paying for. Keying on the chunk's own text rather than on a file's timestamp
// falls out of that: it needs no bookkeeping to go stale, it survives a file
// being touched without being altered, and when a long document changes in one
// paragraph only that paragraph is paid for again.
//
// The cache returned is the one for the build in progress, so anything that has
// gone — a deleted file, an edited paragraph — is dropped rather than kept
// forever. It lives in memory only: the indexer holds nothing durable, and a
// restart rebuilds from the sources by design.
func (i *Index) reuseVectors(chunks []Chunk) (pending []int, dupes []dupChunk, next map[string][]float32) {
	i.mu.RLock()
	cached := i.vecCache
	i.mu.RUnlock()

	next = make(map[string][]float32, len(chunks))
	claimed := make(map[string]bool, len(chunks))
	for idx := range chunks {
		key := textKey(chunks[idx].Text)
		if vec, ok := cached[key]; ok && len(vec) > 0 {
			chunks[idx].vec = vec
			next[key] = vec
			continue
		}
		// Two chunks with identical text embed once. The second cannot read the
		// vector yet — nothing has been embedded — so it is filled in after.
		if claimed[key] {
			dupes = append(dupes, dupChunk{idx: idx, key: key})
			continue
		}
		claimed[key] = true
		pending = append(pending, idx)
	}
	return pending, dupes, next
}

// dupChunk is a chunk waiting on a vector another chunk in the same build is
// already paying for.
type dupChunk struct {
	idx int
	key string
}

// textKey identifies a chunk by its content. A hash rather than the text itself
// so the cache costs 32 bytes a chunk to key, not the document twice over.
func textKey(text string) string {
	sum := sha256.Sum256([]byte(text))
	return string(sum[:])
}

// effectiveSources returns the configured sources, falling back to a single
// globally-scoped local source over the legacy Root when Sources is unset —
// global being the default every unassigned source gets.
func (i *Index) effectiveSources() []SourceSpec {
	if len(i.cfg.Sources) > 0 {
		return i.cfg.Sources
	}
	if i.cfg.Root != "" {
		return []SourceSpec{{Kind: KindLocal, Root: i.cfg.Root, Scope: ScopeGlobal}}
	}
	return nil
}

// ingestLocal walks a local directory and appends the chunks of every file it
// knows how to reduce to text. It returns one Source per file (kind / scope /
// media / content tagged for the UI).
//
// The walk is split in two: collect the paths first, then reduce them. A video
// needs to know whether a caption track sits next to it, and asking that
// question mid-walk would mean a directory read per video (or an ordering
// dependency on when WalkDir happens to reach the sidecar).
func (i *Index) ingestLocal(ctx context.Context, spec SourceSpec, scope string, chunks *[]Chunk) []Source {
	assumed := scopeUnstated(spec.Scope)
	// What the operator called this reference. Every file found under it says so,
	// which is the only way a path relative to a root can be traced back to it.
	origin := displayName(spec)
	// Every file this reference holds is addressed under its id, so two folders
	// that both contain "docs/overview.md" stay two sources. See SourceSpec.ID.
	id := specID(spec)
	var sources []Source
	// Normalised once and shared by every chunk and Source below.
	groups := normalizeGroups(spec.Groups)

	type entry struct {
		path, rel, media string
	}
	var files []entry
	rels := map[string]bool{}
	filepath.WalkDir(spec.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		media := classify(path)
		if media == "" {
			return nil
		}
		rel, _ := filepath.Rel(spec.Root, path)
		files = append(files, entry{path: path, rel: rel, media: media})
		rels[rel] = true
		return nil
	})

	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil {
			sources = append(sources, Source{Path: qualify(id, f.rel), Rel: f.rel, Kind: KindLocal, Scope: scope, declared: scope, assumedGlobal: assumed, abs: f.path, Origin: origin, Groups: groups, Media: f.media, Error: err.Error()})
			continue
		}
		if info.Size() == 0 {
			continue
		}
		sidecar := ""
		if f.media == MediaVideo {
			sidecar = videoSidecar(f.rel, rels)
		}
		red := reduce(f.path, f.rel, f.media, info, sidecar, i.captionerFor(ctx))
		src := Source{Path: qualify(id, f.rel), Rel: f.rel, Kind: KindLocal, Scope: scope, declared: scope, assumedGlobal: assumed, abs: f.path, Origin: origin, Groups: groups, Media: f.media, Content: red.content, Note: red.note}
		if red.err != nil {
			// One unreadable file is recorded and skipped; the rest of the
			// folder still indexes. A build that aborts on the first corrupt
			// PDF would leave the whole knowledge base unavailable.
			src.Error = red.err.Error()
			sources = append(sources, src)
			continue
		}
		parts := chunkText(red.text, i.cfg.MaxChunkRune)
		for _, p := range parts {
			*chunks = append(*chunks, Chunk{Source: qualify(id, f.rel), Text: p, groups: groups, global: scope == ScopeGlobal})
		}
		src.Chunks = len(parts)
		sources = append(sources, src)
	}
	return sources
}

// ingestExternal fetches one HTTPS document, chunks it, and appends the chunks.
// A fetch/transport failure is recorded on the returned Source rather than
// aborting the whole build.
func (i *Index) ingestExternal(ctx context.Context, spec SourceSpec, scope string, chunks *[]Chunk) Source {
	groups := normalizeGroups(spec.Groups)
	src := Source{Path: displayName(spec), Rel: displayName(spec), Kind: KindExternal, Scope: scope, declared: scope, assumedGlobal: scopeUnstated(spec.Scope), URL: spec.URL, Origin: displayName(spec), Groups: groups}
	text, err := i.fetchExternal(ctx, spec.URL)
	if err != nil {
		src.Error = err.Error()
		return src
	}
	parts := chunkText(text, i.cfg.MaxChunkRune)
	for _, p := range parts {
		*chunks = append(*chunks, Chunk{Source: spec.URL, Text: p, groups: groups, global: scope == ScopeGlobal})
	}
	src.Chunks = len(parts)
	return src
}

// maxExternalBytes caps how much of one external document is read.
const maxExternalBytes = 4 << 20 // 4 MiB

// fetchExternal GETs an HTTPS document (HTML is reduced to text). Only https is
// allowed — the indexer is host-side infrastructure, not a sandbox, so this is
// not a sandbox→host egress path; agents still reach knowledge only via the
// gateway's /rag route.
func (i *Index) fetchExternal(ctx context.Context, rawURL string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawURL)), "https://") {
		return "", fmt.Errorf("external source must be an https URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "orchestra-ragindex")
	resp, err := i.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxExternalBytes))
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	text := string(raw)
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "html") || looksHTML(text) {
		text = stripHTML(text)
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("no text content")
	}
	return text, nil
}

// normalizeScope maps free-form scope strings onto the known scopes, defaulting
// to global.
//
// Global is the default because a source that has just been registered belongs
// to no group yet, and a narrower default would make it invisible to every
// scoped search — knowledge that is present, indexed, and unreachable, with
// nothing in the UI to explain why. Restricting a source is then a deliberate
// act, and one gesture: assign it to a group on the Knowledge screen. Nothing
// here has to be edited to make that take effect.
// scopeUnstated reports whether a spec named no scope at all, which is a
// different fact from naming "global": one is a decision, the other is its
// absence, and they part company only when nothing else is known either.
func scopeUnstated(s string) bool {
	return strings.TrimSpace(s) == ""
}

func normalizeScope(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ScopeProject, "proj":
		return ScopeProject
	case ScopeOrganization, "org":
		return ScopeOrganization
	// "manual" was a fourth tier that the filter never treated differently from
	// project or organization, and that nothing selected. A source carrying it
	// normalises to global — the default — rather than to a tier its owner did
	// not choose.
	default:
		return ScopeGlobal
	}
}

// displayName is the label shown for a source: explicit Name, else the URL/root.
// specID is the identifier a reference's files are addressed under, or "" for a
// reference that does not want its files qualified at all.
//
// The shell always supplies an id. A hand-written config may supply a name and
// get that; one that supplies neither is the single-root case that existed
// before any of this, and its paths stay exactly as they were. Falling back to
// the root would qualify them with an absolute filesystem path — unique, but
// not an identifier anyone would choose to store inside a group.
func specID(spec SourceSpec) string {
	if id := strings.TrimSpace(spec.ID); id != "" {
		return id
	}
	return strings.TrimSpace(spec.Name)
}

// qualify joins a reference's id to a path within it. Slash-separated because
// the result is read as a path everywhere it goes — the UI trims it for
// display, and a search result naming it says which folder it came from.
func qualify(id, rel string) string {
	if id == "" {
		return rel
	}
	return id + "/" + rel
}

func displayName(spec SourceSpec) string {
	if spec.Name != "" {
		return spec.Name
	}
	if spec.Kind == KindExternal {
		return spec.URL
	}
	return spec.Root
}

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reWS          = regexp.MustCompile(`[ \t]+`)
	reBlankLines  = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

func looksHTML(s string) bool {
	head := s
	if len(head) > 512 {
		head = head[:512]
	}
	l := strings.ToLower(head)
	return strings.Contains(l, "<html") || strings.Contains(l, "<!doctype html") || strings.Contains(l, "<body")
}

// stripHTML reduces an HTML document to readable text: drop script/style, strip
// tags, unescape the common entities, and collapse whitespace.
func stripHTML(s string) string {
	s = reScriptStyle.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, " ")
	r := strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	s = r.Replace(s)
	s = reWS.ReplaceAllString(s, " ")
	s = reBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// Result is one search hit.
type Result struct {
	Source string  `json:"source"`
	Text   string  `json:"text"`
	Score  float64 `json:"score"`
	// Groups the chunk's source carries. Omitted when untagged rather than
	// emitted as null, which callers would have to guard.
	Groups []string `json:"groups,omitempty"`
}

// Search embeds the query and returns the top-k chunks by cosine similarity,
// considering only chunks the filter permits. A nil filter states no policy and
// searches everything.
//
// Filtering happens before scoring, so k applies to the permitted set: a scoped
// caller gets k results from what it may see, not k results from the whole
// index minus whatever was redacted. That distinction matters — the latter
// leaks the existence of hidden documents through a short result list.
//
// Being a linear scan, this pays nothing for the filter beyond the membership
// test. An approximate index would not have that luxury: filtered nearest
// neighbour is a genuinely harder problem, and moving to one later means
// choosing between per-group indexes and over-fetching with post-filtering.
func (i *Index) Search(ctx context.Context, query string, k int, filter *GroupFilter) ([]Result, error) {
	if k <= 0 {
		k = 5
	}
	i.mu.RLock()
	chunks := i.chunks
	i.mu.RUnlock()
	if len(chunks) == 0 {
		return nil, fmt.Errorf("index is empty (add knowledge and reindex)")
	}
	visible := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		if filter.permits(c.groups, c.global) {
			visible = append(visible, c)
		}
	}
	// Nothing permitted is an answer, not a failure: the caller asked a question
	// its groups do not reach. Return early so a fully-redacted search does not
	// spend an embedding call.
	if len(visible) == 0 {
		return []Result{}, nil
	}
	qv, err := i.embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	q := qv[0]
	scored := make([]Result, 0, len(visible))
	for _, c := range visible {
		scored = append(scored, Result{Source: c.Source, Text: c.Text, Score: cosine(q, c.vec), Groups: c.groups})
	}
	sort.Slice(scored, func(a, b int) bool { return scored[a].Score > scored[b].Score })
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

// Status is the indexer state for the UI.
type Status struct {
	Chunks   int      `json:"chunks"`
	Sources  []Source `json:"sources"`
	Building bool     `json:"building"`
	LastErr  string   `json:"lastError"`
	BuiltAt  string   `json:"builtAt"`
	// EmbedMode is reported so the UI can say when the index was built without
	// a model. Retrieval quality differs enormously between the two and nothing
	// else on screen would reveal which one produced these vectors.
	EmbedMode string `json:"embedMode,omitempty"`
	// From the last build: how many chunks were embedded and how many kept the
	// vector they already had. A rebuild that re-embedded nothing and one that
	// re-embedded everything look identical from the outside otherwise.
	Embedded int `json:"embedded"`
	Reused   int `json:"reused"`
}

func (i *Index) Status() Status {
	i.mu.RLock()
	defer i.mu.RUnlock()
	built := ""
	if !i.builtAt.IsZero() {
		built = i.builtAt.UTC().Format(time.RFC3339)
	}
	// never hand the UI a nil slice: it marshals to JSON null, not [].
	sources := i.sources
	if sources == nil {
		sources = []Source{}
	}
	mode := i.cfg.EmbedMode
	if mode == "" {
		mode = EmbedModeGateway
	}
	return Status{Chunks: len(i.chunks), Sources: sources, Building: i.building, LastErr: i.lastErr, BuiltAt: built, EmbedMode: mode,
		Embedded: i.lastEmbedded, Reused: i.lastReused}
}

/* ── internals ── */

func (i *Index) swap(chunks []Chunk, sources []Source, errMsg string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.chunks = chunks
	i.sources = sources
	i.lastErr = errMsg
	i.builtAt = time.Now()
	// A rebuild re-ingests from config, which carries no host-side group
	// labels; re-apply them here or every permission edit would be lost on the
	// next reindex.
	i.applyGroupsLocked()
}

func (i *Index) setErr(err error) {
	i.mu.Lock()
	i.lastErr = err.Error()
	i.mu.Unlock()
}

// embed produces vectors for the given texts. Offline mode computes them here;
// otherwise they come from the gateway. The split is at this single point so
// indexing and querying can never disagree about which space they are in —
// mixing the two would score every query against noise.
func (i *Index) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if i.embedHook != nil {
		i.embedHook(len(inputs))
	}
	if i.cfg.EmbedMode == EmbedModeOffline {
		return offlineEmbed(inputs), nil
	}
	return i.embedViaGateway(ctx, inputs)
}

// embedViaGateway calls the gateway's embeddings route (OpenAI-compatible shape).
func (i *Index) embedViaGateway(ctx context.Context, inputs []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": i.cfg.EmbedModel, "input": inputs})
	url := strings.TrimRight(i.cfg.Gateway, "/") + i.cfg.EmbedPrefix + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if i.cfg.Session != "" {
		req.Header.Set("X-Orchestra-Session", i.cfg.Session)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if len(out.Data) != len(inputs) {
		return nil, fmt.Errorf("embed returned %d vectors for %d inputs", len(out.Data), len(inputs))
	}
	vecs := make([][]float32, len(out.Data))
	for k := range out.Data {
		vecs[k] = out.Data[k].Embedding
	}
	return vecs, nil
}

// chunkText splits text into ~maxRune-rune windows on line boundaries.
func chunkText(s string, maxRune int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var chunks []string
	var b strings.Builder
	count := 0
	flush := func() {
		if b.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(b.String()))
			b.Reset()
			count = 0
		}
	}
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		n := len([]rune(line))
		if count+n > maxRune && b.Len() > 0 {
			flush()
		}
		b.WriteString(line)
		b.WriteByte('\n')
		count += n + 1
	}
	flush()
	return chunks
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
