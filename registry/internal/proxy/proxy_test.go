package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newTestServer builds a proxy in front of the given ecosystems, with caching
// into a per-test temp dir.
func newTestServer(t *testing.T, ecos []Ecosystem) (*Server, *httptest.Server) {
	t.Helper()
	s := New(Config{
		PublicBase:    "http://proxy.test",
		Ecosystems:    ecos,
		CacheDir:      t.TempDir(),
		MaxCacheBytes: 1 << 20,
	})
	front := httptest.NewServer(s.Handler())
	t.Cleanup(front.Close)
	return s, front
}

func get(t *testing.T, base, path string) *http.Response {
	t.Helper()
	res, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(b)
}

// A registry proxy that could be written to would hand an agent the classic
// supply-chain egress: publish a package, exfiltrate through it. Nothing in the
// service should serve a write method, for any ecosystem, ever.
func TestWriteMethodsAreRefused(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("upstream must never be reached by a %s", r.Method)
	}))
	defer upstream.Close()
	_, front := newTestServer(t, []Ecosystem{{Name: "npm", Prefix: "/npm/", Upstream: upstream.URL}})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequest(method, front.URL+"/npm/some-package", strings.NewReader("{}"))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", method, res.StatusCode)
		}
		if allow := res.Header.Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s: Allow = %q, want \"GET, HEAD\"", method, allow)
		}
	}
}

func TestUnknownPrefixIsNotProxied(t *testing.T) {
	_, front := newTestServer(t, []Ecosystem{{Name: "npm", Prefix: "/npm/", Upstream: "https://registry.npmjs.org"}})
	res := get(t, front.URL, "/rubygems/rails")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

// /pypi/files/ and /pypi/simple/ are different upstreams that share a prefix;
// the longer one has to win or every file download would hit the index.
func TestLongestPrefixWins(t *testing.T) {
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("index"))
	}))
	defer index.Close()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("files"))
	}))
	defer files.Close()

	_, front := newTestServer(t, []Ecosystem{
		{Name: "pypi", Prefix: "/pypi/", Upstream: index.URL},
		{Name: "pypi-files", Prefix: "/pypi/files/", Upstream: files.URL},
	})
	if got := body(t, get(t, front.URL, "/pypi/files/packages/x.whl")); got != "files" {
		t.Errorf("body = %q, want \"files\"", got)
	}
	if got := body(t, get(t, front.URL, "/pypi/simple/requests/")); got != "index" {
		t.Errorf("body = %q, want \"index\"", got)
	}
}

// Metadata embeds absolute download URLs. A sandbox has no route to those
// hosts, so they must come back pointing at the proxy or the install stalls.
func TestMetadataURLsAreRewrittenToTheProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dist":{"tarball":"https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz"}}`))
	}))
	defer upstream.Close()

	_, front := newTestServer(t, []Ecosystem{{
		Name:     "npm",
		Prefix:   "/npm/",
		Upstream: upstream.URL,
		Rewrite:  []Rewrite{{From: "https://registry.npmjs.org/", To: "/npm/"}},
	}})

	got := body(t, get(t, front.URL, "/npm/lodash"))
	want := `{"dist":{"tarball":"http://proxy.test/npm/lodash/-/lodash-4.17.21.tgz"}}`
	if got != want {
		t.Errorf("rewritten body =\n%s\nwant\n%s", got, want)
	}
}

// Immutable artifacts are cached so disposable sandboxes do not re-download the
// same tarball on every run. Mutable metadata must not be.
func TestImmutableArtifactsAreCachedAndMetadataIsNot(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("tarball-bytes"))
	}))
	defer upstream.Close()

	_, front := newTestServer(t, []Ecosystem{{
		Name:      "npm",
		Prefix:    "/npm/",
		Upstream:  upstream.URL,
		Immutable: []string{"/-/"},
	}})

	for i := 0; i < 2; i++ {
		res := get(t, front.URL, "/npm/lodash/-/lodash-4.17.21.tgz")
		if got := body(t, res); got != "tarball-bytes" {
			t.Fatalf("request %d: body = %q", i, got)
		}
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d, want 1 (second request should be a cache hit)", hits)
	}
	if h := get(t, front.URL, "/npm/lodash/-/lodash-4.17.21.tgz").Header.Get("X-Orchestra-Cache"); h != "hit" {
		t.Errorf("X-Orchestra-Cache = %q, want \"hit\"", h)
	}

	// A version listing is mutable: caching it would pin an install to a stale
	// view of the registry.
	hits = 0
	for i := 0; i < 2; i++ {
		get(t, front.URL, "/npm/lodash")
	}
	if hits != 2 {
		t.Errorf("metadata upstream hits = %d, want 2 (metadata must not be cached)", hits)
	}
}

// A registry that redirects downloads to a CDN is normal; one that redirects
// the proxy onto an arbitrary origin is how a fixed-upstream design would leak
// into a general-purpose fetcher.
func TestRedirectsStayWithinTheDeclaredHosts(t *testing.T) {
	var redirectTo string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/redirect") {
			http.Redirect(w, r, redirectTo, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("cdn-bytes"))
	}))
	defer upstream.Close()

	upstreamHost := mustHost(t, upstream.URL)
	_, front := newTestServer(t, []Ecosystem{{
		Name:       "crates",
		Prefix:     "/crates/",
		Upstream:   upstream.URL,
		AllowHosts: []string{upstreamHost},
	}})

	// Same-host redirect: followed.
	redirectTo = upstream.URL + "/artifact"
	res := get(t, front.URL, "/crates/redirect")
	if got := body(t, res); got != "cdn-bytes" {
		t.Errorf("allowed redirect: body = %q, want \"cdn-bytes\"", got)
	}

	// Off-host redirect: refused before the connection is made, so the origin is
	// never contacted at all. The metadata endpoint is the canonical target of
	// this trick — a compromised upstream bouncing the proxy at it would turn a
	// fixed-upstream relay into a general-purpose fetcher.
	for _, target := range []string{"http://evil.example/artifact", "http://169.254.169.254/latest/meta-data/"} {
		redirectTo = target
		res = get(t, front.URL, "/crates/redirect")
		if res.StatusCode != http.StatusBadGateway {
			t.Errorf("redirect to %s: status = %d, want 502", target, res.StatusCode)
		}
		if got := body(t, res); !strings.Contains(got, "outside the crates upstream allowlist") {
			t.Errorf("redirect to %s: body = %q, want the allowlist refusal", target, got)
		}
	}
}

// The proxy holds no registry credentials and must not become a way to replay
// whatever a sandbox chooses to send.
func TestInboundCredentialsAreNotForwarded(t *testing.T) {
	var gotAuth, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	_, front := newTestServer(t, []Ecosystem{{Name: "npm", Prefix: "/npm/", Upstream: upstream.URL}})
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/npm/lodash", nil)
	req.Header.Set("Authorization", "Bearer stolen-token")
	req.Header.Set("Cookie", "session=abc")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()

	if gotAuth != "" {
		t.Errorf("Authorization forwarded upstream: %q", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("Cookie forwarded upstream: %q", gotCookie)
	}
}

func TestQueryStringAndPathReachUpstreamIntact(t *testing.T) {
	var gotURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	_, front := newTestServer(t, []Ecosystem{{Name: "go", Prefix: "/go/", Upstream: upstream.URL}})
	get(t, front.URL, "/go/github.com/!burnt!sushi/toml/@v/list?x=1")
	if want := "/github.com/!burnt!sushi/toml/@v/list?x=1"; gotURL != want {
		t.Errorf("upstream URL = %q, want %q", gotURL, want)
	}
}

// The go command asks the proxy whether it relays the checksum database, and
// treats anything but a 200 as "it doesn't" — then dials sum.golang.org
// directly, which inside the egress island is a DNS failure rather than a
// fallback. The proxy has to answer that question itself.
func TestSumDBSupportedIsAnsweredWithoutAnUpstream(t *testing.T) {
	reached := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		_, _ = w.Write([]byte("upstream"))
	}))
	defer upstream.Close()

	_, front := newTestServer(t, []Ecosystem{{
		Name:     "go-sumdb",
		Prefix:   "/go/sumdb/sum.golang.org/",
		Upstream: upstream.URL,
		AlwaysOK: []string{"supported"},
	}})

	res := get(t, front.URL, "/go/sumdb/sum.golang.org/supported")
	if res.StatusCode != http.StatusOK {
		t.Errorf("supported = %d, want 200", res.StatusCode)
	}
	if reached != 0 {
		t.Error("the supported probe was forwarded upstream; it is a question about this proxy")
	}

	// Everything else under the prefix still goes upstream.
	if got := body(t, get(t, front.URL, "/go/sumdb/sum.golang.org/lookup/x@v1")); got != "upstream" {
		t.Errorf("lookup body = %q, want it proxied upstream", got)
	}
}

// The sum database has to win over the module proxy, or checksum traffic is
// sent to proxy.golang.org, which does not serve it.
func TestSumDBPrefixOutranksTheModuleProxy(t *testing.T) {
	var sumdb, modproxy *Ecosystem
	for i, e := range DefaultEcosystems() {
		switch e.Name {
		case "go-sumdb":
			sumdb = &DefaultEcosystems()[i]
		case "go":
			modproxy = &DefaultEcosystems()[i]
		}
	}
	if sumdb == nil || modproxy == nil {
		t.Fatal("the go module proxy and sum database must both be configured")
	}
	if len(sumdb.Prefix) <= len(modproxy.Prefix) {
		t.Errorf("sumdb prefix %q must be longer than %q to win the match", sumdb.Prefix, modproxy.Prefix)
	}
	s := New(Config{Ecosystems: DefaultEcosystems()})
	eco, rest := s.match("/go/sumdb/sum.golang.org/lookup/github.com/google/uuid@v1.6.0")
	if eco == nil || eco.Name != "go-sumdb" {
		t.Fatalf("sumdb path matched %v, want go-sumdb", eco)
	}
	if rest != "lookup/github.com/google/uuid@v1.6.0" {
		t.Errorf("rest = %q", rest)
	}
	// Nothing may disable checksum verification on the sandbox side either; that
	// is asserted in the sandbox controller's TestSandboxEnvForcesTheRegistryProxy.
}

func TestHostAllowed(t *testing.T) {
	eco := Ecosystem{Upstream: "https://crates.io/api", AllowHosts: []string{"static.crates.io"}}
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://crates.io/api/v1/crates/serde/1.0.0/download", true},
		{"https://static.crates.io/crates/serde/serde-1.0.0.crate", true},
		{"https://crates.io.evil.example/x", false},
		{"https://169.254.169.254/latest/meta-data/", false},
		{"file:///etc/passwd", false},
		// userinfo must not be mistaken for the host
		{"https://static.crates.io@evil.example/x", false},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatalf("parsing %q: %v", c.raw, err)
		}
		if got := hostAllowed(u, eco); got != c.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestDefaultEcosystemsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range DefaultEcosystems() {
		if !strings.HasPrefix(e.Prefix, "/") || !strings.HasSuffix(e.Prefix, "/") {
			t.Errorf("%s: prefix %q must start and end with /", e.Name, e.Prefix)
		}
		if seen[e.Prefix] {
			t.Errorf("duplicate prefix %q", e.Prefix)
		}
		seen[e.Prefix] = true
		u, err := url.Parse(e.Upstream)
		if err != nil || u.Scheme != "https" {
			t.Errorf("%s: upstream %q must be an https URL", e.Name, e.Upstream)
		}
		// Every rewrite must land on a prefix this proxy actually serves,
		// otherwise the rewritten URL 404s inside the egress island.
		for _, rw := range e.Rewrite {
			if !servesPrefix(rw.To) {
				t.Errorf("%s: rewrite target %q is not served by any ecosystem", e.Name, rw.To)
			}
		}
	}
}

func servesPrefix(to string) bool {
	for _, e := range DefaultEcosystems() {
		if strings.HasPrefix(to, e.Prefix) {
			return true
		}
	}
	return false
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return u.Hostname()
}
