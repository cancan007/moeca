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
	Source string    `json:"source"`
	Text   string    `json:"text"`
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
}

// Source kinds and scopes.
const (
	KindLocal    = "local"
	KindExternal = "external"

	// ScopeGlobal is the default: knowledge every task may retrieve, whatever
	// groups its run was granted. The narrower scopes below are subject to the
	// group filter; global is not.
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
	// Groups are permission labels; a scoped search sees this source only if it
	// permits one of them. A source with no groups is visible to unscoped
	// searches only — unless its scope is global, which bypasses the filter
	// entirely. See groups.go.
	Groups []string `json:"groups,omitempty"`
}

// Index is the live, mutable in-memory vector store.
type Index struct {
	cfg  Config
	http *http.Client

	// membership holds the host-pushed source→groups mapping, re-applied after
	// every build so a reindex cannot silently drop every permission label.
	membership membership

	mu       sync.RWMutex
	chunks   []Chunk
	sources  []Source
	building bool
	lastErr  string
	builtAt  time.Time
}

// Source is a per-source summary for the UI. Kind/Scope let the UI group and
// badge sources (local vs external HTTPS, project vs organization); URL is set
// for external sources; Error carries a per-source fetch failure without failing
// the whole build.
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
		sources = append(sources, i.ingestLocal(spec, scope, &chunks)...)
	}
	if len(chunks) == 0 {
		i.swap(nil, sources, "")
		return nil
	}

	// Embed in batches.
	texts := make([]string, len(chunks))
	for k, c := range chunks {
		texts[k] = c.Text
	}
	const batch = 64
	for start := 0; start < len(texts); start += batch {
		end := start + batch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := i.embed(ctx, texts[start:end])
		if err != nil {
			i.setErr(err)
			return err
		}
		for k := range vecs {
			chunks[start+k].vec = vecs[k]
		}
	}
	i.swap(chunks, sources, "")
	return nil
}

// effectiveSources returns the configured sources, falling back to a single
// project-scoped local source over the legacy Root when Sources is unset.
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
func (i *Index) ingestLocal(spec SourceSpec, scope string, chunks *[]Chunk) []Source {
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
			sources = append(sources, Source{Path: f.rel, Kind: KindLocal, Scope: scope, Groups: groups, Media: f.media, Error: err.Error()})
			continue
		}
		if info.Size() == 0 {
			continue
		}
		sidecar := ""
		if f.media == MediaVideo {
			sidecar = videoSidecar(f.rel, rels)
		}
		red := reduce(f.path, f.rel, f.media, info, sidecar)
		src := Source{Path: f.rel, Kind: KindLocal, Scope: scope, Groups: groups, Media: f.media, Content: red.content, Note: red.note}
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
			*chunks = append(*chunks, Chunk{Source: f.rel, Text: p, groups: groups, global: scope == ScopeGlobal})
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
	src := Source{Path: displayName(spec), Kind: KindExternal, Scope: scope, URL: spec.URL, Groups: groups}
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
// act: give it a narrower scope and assign it to groups.
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
	return Status{Chunks: len(i.chunks), Sources: sources, Building: i.building, LastErr: i.lastErr, BuiltAt: built, EmbedMode: mode}
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
