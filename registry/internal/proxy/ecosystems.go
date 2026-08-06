package proxy

// The built-in ecosystem table.
//
// Each entry pins a fixed upstream and, where the registry hands out absolute
// download URLs, the rewrites that point those URLs back at this proxy. Getting
// the rewrites right is what makes an install actually complete: a sandbox that
// followed `dist.tarball` or a files.pythonhosted.org link verbatim would be
// dialling a host it has no route to, and the install would hang rather than
// fail cleanly.
//
// Immutable markers select what may be cached. They are chosen so that only
// content-addressed, publish-once artifacts match — never a version listing:
//
//   - npm    "/-/"    is the tarball segment (/<pkg>/-/<pkg>-<ver>.tgz)
//   - pypi   "/packages/" is the file store; the /simple/ index stays uncached
//   - go     "/@v/v"  matches v1.2.3.{info,mod,zip} but NOT "/@v/list"
//   - crates "/download" is the fixed download verb
func DefaultEcosystems() []Ecosystem {
	return []Ecosystem{
		{
			Name:       "npm",
			Prefix:     "/npm/",
			Upstream:   "https://registry.npmjs.org",
			AllowHosts: []string{"registry.npmjs.org"},
			Rewrite:    []Rewrite{{From: "https://registry.npmjs.org/", To: "/npm/"}},
			Immutable:  []string{"/-/"},
		},
		{
			// pip's index. Its HTML links point at the separate file host, so the
			// links are rewritten onto the /pypi/files/ ecosystem below.
			Name:       "pypi",
			Prefix:     "/pypi/simple/",
			Upstream:   "https://pypi.org/simple",
			AllowHosts: []string{"pypi.org"},
			Rewrite:    []Rewrite{{From: "https://files.pythonhosted.org/", To: "/pypi/files/"}},
		},
		{
			Name:       "pypi-files",
			Prefix:     "/pypi/files/",
			Upstream:   "https://files.pythonhosted.org",
			AllowHosts: []string{"files.pythonhosted.org"},
			Immutable:  []string{"/packages/"},
		},
		{
			// The checksum database, which must be listed BEFORE (longer prefix
			// than) the module proxy below.
			//
			// GOSUMDB is deliberately left on: turning it off would be the easy
			// way to make `go get` work inside the island and would also turn off
			// the one mechanism that notices a module's bytes changing under a
			// fixed version. So the sum database has to be reachable — and it
			// cannot be reached through proxy.golang.org, which does not relay it
			// (`/sumdb/...` there is a 404). It is proxied here directly instead.
			//
			// `supported` is answered locally: the go command is asking whether
			// THIS proxy will relay the database, and a non-200 sends it dialling
			// sum.golang.org directly, which inside the island fails as a DNS
			// error rather than a clean fallback.
			Name:       "go-sumdb",
			Prefix:     "/go/sumdb/sum.golang.org/",
			Upstream:   "https://sum.golang.org",
			AllowHosts: []string{"sum.golang.org"},
			AlwaysOK:   []string{"supported"},
			// Tiles are an append-only transparency log: a given tile's bytes
			// never change. Lookups embed a signed tree head that does, so they
			// stay uncached.
			Immutable: []string{"/tile/"},
		},
		{
			// The GOPROXY protocol is entirely proxy-relative, so no rewriting is
			// needed.
			Name:       "go",
			Prefix:     "/go/",
			Upstream:   "https://proxy.golang.org",
			AllowHosts: []string{"proxy.golang.org"},
			Immutable:  []string{"/@v/v"},
		},
		{
			// Cargo's sparse index. Its config.json advertises an absolute `dl`
			// endpoint on crates.io, rewritten onto the /crates/api/ entry below.
			Name:       "crates-index",
			Prefix:     "/crates/index/",
			Upstream:   "https://index.crates.io",
			AllowHosts: []string{"index.crates.io"},
			Rewrite:    []Rewrite{{From: "https://crates.io/api/v1/crates", To: "/crates/api/v1/crates"}},
		},
		{
			// Downloads 302 to the static CDN, which is why it is an allowed
			// redirect host rather than a second prefix.
			Name:       "crates",
			Prefix:     "/crates/api/",
			Upstream:   "https://crates.io/api",
			AllowHosts: []string{"crates.io", "static.crates.io"},
			Immutable:  []string{"/download"},
		},
	}
}
