// Package proxy implements Orchestra's package-registry proxy: the one way a
// strict sandbox can install dependencies without ever getting a route to the
// internet.
//
// The isolation contract is the same one the gateway serves, applied to a second
// kind of traffic. A strict sandbox sits on the `--internal` egress island and
// has no route off it; this proxy is dual-homed (egress + upstream) exactly like
// the gateway, so `npm install` / `pip install` / `go mod download` resolve to a
// container the sandbox CAN reach, which then makes a NEW request upstream on
// its own NIC. Nothing about the island changes.
//
// Three properties make this a security *gain* rather than a hole punched in the
// boundary:
//
//   - Read-only by construction. Only GET and HEAD are served. There is no code
//     path that can PUT/POST to a registry, so a compromised agent cannot
//     publish a package, overwrite a version, or exfiltrate through a publish
//     endpoint — the classic supply-chain egress.
//   - No caller-supplied destinations. Upstreams are fixed per ecosystem in
//     config; the request path selects an ecosystem, never a host. Unlike the
//     gateway's /fetch route there is simply no dynamic-target surface to defend,
//     and redirects may only land on that ecosystem's declared hosts.
//   - Dependency fetches become auditable. Every artifact an agent pulls in
//     passes this one chokepoint and is logged, so the place supply-chain
//     attacks actually land is on the record.
//
// The proxy holds no credentials: these are public registries and inbound
// Authorization/Cookie headers are dropped rather than forwarded.
package proxy

import (
	"encoding/json"
	"fmt"
	"context"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Rewrite maps an absolute upstream URL prefix to a proxy-relative one.
//
// This is what makes the proxy actually usable rather than merely reachable.
// Registry metadata embeds absolute download URLs (npm's `dist.tarball`, the
// PyPI simple index's links to files.pythonhosted.org, crates' `dl`), and a
// sandbox that followed them would be dialling a host it has no route to. Every
// such URL is rewritten to point back at this proxy before the body is served.
type Rewrite struct {
	// From is the absolute upstream prefix as it appears in the body.
	From string `json:"from"`
	// To is the proxy-relative prefix to replace it with, e.g. "/npm/".
	To string `json:"to"`
}

// Ecosystem is one package registry the proxy serves. Upstream is fixed here,
// in host-owned config — a request selects an ecosystem by path prefix and can
// never name a host.
type Ecosystem struct {
	// Name identifies the ecosystem in logs, e.g. "npm".
	Name string `json:"name"`
	// Prefix is the path prefix sandboxes call, e.g. "/npm/".
	Prefix string `json:"prefix"`
	// Upstream is the fixed registry origin, e.g. "https://registry.npmjs.org".
	Upstream string `json:"upstream"`
	// AllowHosts bounds where a redirect from Upstream may land (the upstream's
	// own host is always allowed). Registries routinely redirect downloads to a
	// CDN host; anything else is refused rather than followed.
	AllowHosts []string `json:"allowHosts"`
	// Rewrite rewrites absolute upstream URLs in textual responses back to this
	// proxy. See Rewrite.
	Rewrite []Rewrite `json:"rewrite"`
	// Immutable marks path substrings whose bytes never change once published
	// (tarballs, wheels, module zips). Only these are cached.
	Immutable []string `json:"immutable"`
	// AlwaysOK lists prefix-relative paths this ecosystem answers ITSELF with an
	// empty 200, without contacting any upstream.
	//
	// This exists for one specific handshake: the go command asks
	// `$GOPROXY/sumdb/<name>/supported` to find out whether the proxy will relay
	// the checksum database, and falls back to dialling sum.golang.org directly
	// if the answer is not 200 — which, inside the egress island, means the
	// build fails with a DNS error instead of verifying checksums. The question
	// is about THIS proxy's capabilities, so this proxy is the thing that has to
	// answer it.
	AlwaysOK []string `json:"alwaysOK"`
}

// answersLocally reports whether a prefix-relative path is one this ecosystem
// answers itself.
func (e Ecosystem) answersLocally(rest string) bool {
	rest = strings.TrimPrefix(rest, "/")
	for _, p := range e.AlwaysOK {
		if rest == strings.TrimPrefix(p, "/") {
			return true
		}
	}
	return false
}

// immutablePath reports whether a request path may be cached for this ecosystem.
func (e Ecosystem) immutablePath(p string) bool {
	for _, marker := range e.Immutable {
		if marker != "" && strings.Contains(p, marker) {
			return true
		}
	}
	return false
}

// Config is the registry proxy configuration.
type Config struct {
	// Listen address. The container binds 0.0.0.0 and is reachable only from the
	// networks it is attached to.
	Listen string `json:"listen"`
	// PublicBase is this proxy's origin as a sandbox sees it, e.g.
	// "http://orchestra-registry:8791". Rewritten URLs are built from it.
	PublicBase string `json:"publicBase"`
	// Ecosystems are the registries served. An empty list means the proxy serves
	// nothing (fail closed).
	Ecosystems []Ecosystem `json:"ecosystems"`
	// CacheDir / MaxCacheBytes bound the immutable-artifact cache. 0 disables it.
	CacheDir      string `json:"cacheDir"`
	MaxCacheBytes int64  `json:"maxCacheBytes"`
	// MaxMetaBytes caps a buffered (rewritable) metadata response;
	// MaxArtifactBytes caps a streamed artifact.
	MaxMetaBytes     int64 `json:"maxMetaBytes"`
	MaxArtifactBytes int64 `json:"maxArtifactBytes"`
	// RequestTimeoutSec bounds one upstream fetch.
	RequestTimeoutSec int `json:"requestTimeoutSec"`
}

// Defaults applied by normalize when a field is zero.
const (
	DefaultListen           = "0.0.0.0:8791"
	DefaultPublicBase       = "http://orchestra-registry:8791"
	DefaultMaxCacheBytes    = 4 << 30  // 4 GiB of artifacts
	DefaultMaxMetaBytes     = 64 << 20 // a large packument still fits
	DefaultMaxArtifactBytes = 512 << 20
	DefaultTimeoutSec       = 120
	maxRedirects            = 5
)

func (c Config) normalize() Config {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if c.PublicBase == "" {
		c.PublicBase = DefaultPublicBase
	}
	c.PublicBase = strings.TrimRight(c.PublicBase, "/")
	if c.MaxCacheBytes == 0 {
		c.MaxCacheBytes = DefaultMaxCacheBytes
	}
	if c.MaxMetaBytes <= 0 {
		c.MaxMetaBytes = DefaultMaxMetaBytes
	}
	if c.MaxArtifactBytes <= 0 {
		c.MaxArtifactBytes = DefaultMaxArtifactBytes
	}
	if c.RequestTimeoutSec <= 0 {
		c.RequestTimeoutSec = DefaultTimeoutSec
	}
	return c
}

// Server is the registry proxy.
type Server struct {
	cfg    Config
	client *http.Client
	cache  *Cache
}

// New builds a Server. The HTTP client refuses to follow a redirect off the
// requesting ecosystem's declared hosts, so an upstream cannot walk the proxy
// onto an arbitrary origin.
func New(cfg Config) *Server {
	cfg = cfg.normalize()
	s := &Server{
		cfg:   cfg,
		cache: NewCache(cfg.CacheDir, cfg.MaxCacheBytes),
	}
	s.client = &http.Client{
		Timeout:       time.Duration(cfg.RequestTimeoutSec) * time.Second,
		CheckRedirect: s.checkRedirect,
	}
	return s
}

// ecosystemKey is the request-context key carrying the ecosystem through a
// redirect chain, so checkRedirect knows which host list applies.
type ecosystemKey struct{}

// contextWithEcosystem tags an outbound request's context with the ecosystem it
// belongs to. http.Client re-uses the context for every hop of a redirect chain,
// which is how checkRedirect can still tell which allowlist applies.
func contextWithEcosystem(r *http.Request, eco *Ecosystem) context.Context {
	return context.WithValue(r.Context(), ecosystemKey{}, eco)
}

// checkRedirect allows a redirect only to a host the ecosystem declared.
func (s *Server) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("too many redirects")
	}
	eco, _ := req.Context().Value(ecosystemKey{}).(*Ecosystem)
	if eco == nil {
		return fmt.Errorf("redirect with no ecosystem context")
	}
	if !hostAllowed(req.URL, *eco) {
		return fmt.Errorf("redirect to %q is outside the %s upstream allowlist", req.URL.Host, eco.Name)
	}
	// Credentials are never carried across a redirect.
	req.Header.Del("Authorization")
	req.Header.Del("Cookie")
	return nil
}

// hostAllowed reports whether u's host is the ecosystem's own upstream host or
// one of its declared redirect hosts. Matching is on the hostname, so a port or
// userinfo cannot smuggle a different origin past it.
func hostAllowed(u *url.URL, eco Ecosystem) bool {
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if up, err := url.Parse(eco.Upstream); err == nil && strings.EqualFold(host, up.Hostname()) {
		return true
	}
	for _, h := range eco.AllowHosts {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// Handler builds the routes: /health for liveness, everything else is a
// registry fetch dispatched by path prefix.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "ecosystems": s.names()})
	})
	mux.HandleFunc("/", s.handle)
	return mux
}

func (s *Server) names() []string {
	out := make([]string, 0, len(s.cfg.Ecosystems))
	for _, e := range s.cfg.Ecosystems {
		out = append(out, e.Name)
	}
	return out
}

// Run serves until the process exits.
func (s *Server) Run() error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	return srv.ListenAndServe()
}

// match finds the ecosystem serving a request path (longest prefix wins) and
// returns the remainder of the path.
func (s *Server) match(p string) (*Ecosystem, string) {
	var best *Ecosystem
	var rest string
	for i := range s.cfg.Ecosystems {
		eco := &s.cfg.Ecosystems[i]
		if eco.Prefix == "" || !strings.HasPrefix(p, eco.Prefix) {
			continue
		}
		if best == nil || len(eco.Prefix) > len(best.Prefix) {
			best = eco
			rest = strings.TrimPrefix(p, eco.Prefix)
		}
	}
	return best, rest
}

// accessLog is one proxied fetch, emitted as a JSON line. Dependency downloads
// are where supply-chain attacks land, so every one is on the record.
type accessLog struct {
	Time      string `json:"time"`
	Ecosystem string `json:"ecosystem"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Upstream  string `json:"upstream,omitempty"`
	Status    int    `json:"status"`
	Bytes     int64  `json:"bytes"`
	Cached    bool   `json:"cached,omitempty"`
	Rewritten bool   `json:"rewritten,omitempty"`
	Duration  int64  `json:"durationMs"`
	Err       string `json:"err,omitempty"`
}

func (l accessLog) emit(start time.Time) {
	l.Time = start.UTC().Format(time.RFC3339)
	l.Duration = time.Since(start).Milliseconds()
	if b, err := json.Marshal(l); err == nil {
		log.Println(string(b))
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	entry := accessLog{Method: r.Method, Path: r.URL.Path}

	// Read-only by construction: there is no write path to a registry here, so
	// an agent cannot publish, overwrite or delete a package. This check is the
	// whole of that guarantee — keep it first and unconditional.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		entry.Status = http.StatusMethodNotAllowed
		entry.Err = "write methods are never proxied to a package registry"
		entry.emit(start)
		w.Header().Set("Allow", "GET, HEAD")
		writeErr(w, http.StatusMethodNotAllowed, entry.Err)
		return
	}

	eco, rest := s.match(r.URL.Path)
	if eco == nil {
		entry.Status = http.StatusNotFound
		entry.Err = "no ecosystem serves this path"
		entry.emit(start)
		writeErr(w, http.StatusNotFound, "unknown registry path (served: "+strings.Join(s.names(), ", ")+")")
		return
	}
	entry.Ecosystem = eco.Name

	if eco.answersLocally(rest) {
		entry.Status = http.StatusOK
		entry.emit(start)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}

	target := strings.TrimRight(eco.Upstream, "/") + "/" + strings.TrimPrefix(rest, "/")
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	entry.Upstream = target

	// Cache: immutable artifacts only, GET only.
	key := cacheKey(eco.Name, r.URL.Path)
	cacheable := r.Method == http.MethodGet && eco.immutablePath(r.URL.Path)
	if cacheable {
		if path, ctype, ok := s.cache.Get(key); ok {
			entry.Cached = true
			n := serveFile(w, path, ctype)
			entry.Status = 200
			entry.Bytes = n
			entry.emit(start)
			return
		}
	}

	resp, err := s.fetch(r, eco, target)
	if err != nil {
		entry.Status = http.StatusBadGateway
		entry.Err = err.Error()
		entry.emit(start)
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	entry.Status = resp.StatusCode

	copyResponseHeaders(w, resp)

	if r.Method == http.MethodHead {
		w.WriteHeader(resp.StatusCode)
		entry.emit(start)
		return
	}

	if cacheable {
		n, err := s.streamArtifact(w, resp, key)
		entry.Bytes = n
		if err != nil {
			entry.Err = err.Error()
		}
		entry.emit(start)
		return
	}

	// Metadata: buffer it so the embedded absolute URLs can be pointed back at
	// this proxy — a sandbox has no route to the registry's own download host.
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.MaxMetaBytes+1))
	if err != nil {
		entry.Err = err.Error()
		entry.emit(start)
		writeErr(w, http.StatusBadGateway, "reading upstream response: "+err.Error())
		return
	}
	if int64(len(body)) > s.cfg.MaxMetaBytes {
		entry.Status = http.StatusBadGateway
		entry.Err = "metadata response exceeds maxMetaBytes"
		entry.emit(start)
		writeErr(w, http.StatusBadGateway, entry.Err)
		return
	}
	rewritten, changed := applyRewrites(body, *eco, s.cfg.PublicBase)
	entry.Rewritten = changed
	entry.Bytes = int64(len(rewritten))
	w.Header().Set("Content-Length", strconv.Itoa(len(rewritten)))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(rewritten)
	entry.emit(start)
}

// fetch issues the upstream request. It builds a NEW request rather than
// forwarding the sandbox's: inbound credentials and hop-by-hop headers are
// dropped, not relayed, and the ecosystem travels in the context so the
// redirect check knows which host list applies.
func (s *Server) fetch(r *http.Request, eco *Ecosystem, target string) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("bad upstream url: %w", err)
	}
	if !hostAllowed(u, *eco) {
		return nil, fmt.Errorf("upstream %q is not allowed for %s", u.Host, eco.Name)
	}
	ctx := contextWithEcosystem(r, eco)
	req, err := http.NewRequestWithContext(ctx, r.Method, target, nil)
	if err != nil {
		return nil, err
	}
	// Only these travel upstream. Notably absent: Authorization, Cookie, and
	// Accept-Encoding (left to the transport so bodies arrive decoded and can be
	// rewritten).
	if a := r.Header.Get("Accept"); a != "" {
		req.Header.Set("Accept", a)
	}
	req.Header.Set("User-Agent", "orchestra-registry-proxy")
	return s.client.Do(req)
}

// streamArtifact copies an immutable artifact to the client and, on a complete
// 200, commits the same bytes to the cache. It streams through a temp file
// rather than buffering, so a 300 MB image layer does not become 300 MB of RSS.
func (s *Server) streamArtifact(w http.ResponseWriter, resp *http.Response, key string) (int64, error) {
	if resp.StatusCode != http.StatusOK || s.cache == nil {
		w.WriteHeader(resp.StatusCode)
		return io.Copy(w, io.LimitReader(resp.Body, s.cfg.MaxArtifactBytes))
	}
	tmp, err := os.CreateTemp(s.cfg.CacheDir, "dl-*")
	if err != nil {
		w.WriteHeader(resp.StatusCode)
		return io.Copy(w, io.LimitReader(resp.Body, s.cfg.MaxArtifactBytes))
	}
	tmpName := tmp.Name()
	w.WriteHeader(resp.StatusCode)
	n, copyErr := io.Copy(io.MultiWriter(w, tmp), io.LimitReader(resp.Body, s.cfg.MaxArtifactBytes))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpName) // never cache a truncated artifact
		if copyErr != nil {
			return n, copyErr
		}
		return n, closeErr
	}
	s.cache.Put(key, resp.Header.Get("Content-Type"), tmpName, n)
	return n, nil
}

// applyRewrites points absolute upstream URLs in a body back at this proxy,
// reporting whether anything was replaced.
func applyRewrites(body []byte, eco Ecosystem, publicBase string) ([]byte, bool) {
	out := string(body)
	changed := false
	for _, rw := range eco.Rewrite {
		if rw.From == "" || rw.To == "" || !strings.Contains(out, rw.From) {
			continue
		}
		out = strings.ReplaceAll(out, rw.From, publicBase+rw.To)
		changed = true
	}
	if !changed {
		return body, false
	}
	return []byte(out), true
}

// copyResponseHeaders forwards only headers that describe the entity. Transport
// framing (Content-Length, Content-Encoding, Transfer-Encoding) is deliberately
// NOT copied: the transport may have decoded the body, and rewriting changes its
// length, so a copied value would be a lie.
func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for _, h := range []string{"Content-Type", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
}

// serveFile writes a cached body straight from disk and returns its size.
func serveFile(w http.ResponseWriter, path, contentType string) int64 {
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cache read failed")
		return 0
	}
	defer f.Close()
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	w.Header().Set("X-Orchestra-Cache", "hit")
	n, _ := io.Copy(w, f)
	return n
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
