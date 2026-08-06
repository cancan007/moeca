# registry

Orchestra's **package-registry proxy** — the dependency-fetch counterpart to the
gateway.

A strict sandbox sits on the `--internal` egress island and has **no route to the
internet**, so `npm install` / `pip install` / `go mod download` cannot work by
themselves. This service is dual-homed exactly like the gateway (egress +
upstream), so those fetches resolve to a container the sandbox *can* reach, which
then issues a **new** request upstream on its own NIC. The island is unchanged.

Default listen: `0.0.0.0:8791`, reachable from a sandbox as
`http://orchestra-registry:8791`.

## Why this is a security gain, not a hole

| Property | Consequence |
| --- | --- |
| **GET / HEAD only** | There is no code path that writes to a registry. A compromised agent cannot publish a package, overwrite a version, or exfiltrate through a publish endpoint. |
| **No caller-supplied destinations** | Upstreams are fixed per ecosystem in config; the request path selects an *ecosystem*, never a host. Unlike the gateway's `/fetch` there is no dynamic-target surface at all. |
| **Bounded redirects** | A redirect may only land on that ecosystem's declared hosts (CDNs), so an upstream cannot walk the proxy onto an arbitrary origin. |
| **No credentials** | Public registries only; inbound `Authorization` / `Cookie` are dropped, not relayed. |
| **Every fetch logged** | Dependency downloads are where supply-chain attacks land — now they pass one chokepoint and are on the record. |

## Ecosystems

Built in (`internal/proxy/ecosystems.go`); override with `ecosystems` in the
config to add or replace entries.

| Prefix | Upstream | Notes |
| --- | --- | --- |
| `/npm/` | `registry.npmjs.org` | `dist.tarball` URLs rewritten back to the proxy |
| `/pypi/simple/` | `pypi.org/simple` | index links rewritten onto `/pypi/files/` |
| `/pypi/files/` | `files.pythonhosted.org` | wheels/sdists |
| `/go/` | `proxy.golang.org` | proxy-relative protocol; the sum database is fetched through the same proxy, so **checksum verification stays on** |
| `/crates/index/` | `index.crates.io` | sparse index; `dl` rewritten onto `/crates/api/` |
| `/crates/api/` | `crates.io/api` | downloads 302 to `static.crates.io` (an allowed redirect host) |

### URL rewriting is what makes it work

Registry metadata embeds **absolute** download URLs. A sandbox that followed
`https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz` verbatim would be
dialling a host it has no route to. Every such URL is rewritten to point back at
the proxy before the body is served.

## The cache lives here, deliberately

Sandboxes are disposable: their toolchain caches are tmpfs and die with the
container. Without a cache every run re-downloads its whole dependency tree.

The durable cache is therefore held **by the proxy**, not in a volume shared
between sandboxes. A shared writable cache mount would let one task plant bytes
that another task later executes — the same cross-task contamination the
worktree-per-task rule exists to prevent. Here a sandbox can only *read* the
cache, over HTTP, through this proxy.

Only **immutable** artifacts are cached (tarballs, wheels, module zips); version
listings never are, so an install is never pinned to a stale view of a registry.
The budget is bounded (`maxCacheBytes`, default 4 GiB) with oldest-first
eviction.

## Config

```json
{
  "listen": "0.0.0.0:8791",
  "publicBase": "http://orchestra-registry:8791",
  "cacheDir": "/cache",
  "maxCacheBytes": 4294967296
}
```

Omitting `ecosystems` uses the built-in table above. Env overrides:
`ORCHESTRA_LISTEN`, `ORCHESTRA_REGISTRY_PUBLIC_BASE`,
`ORCHESTRA_REGISTRY_CACHE_DIR`, `ORCHESTRA_REGISTRY_MAX_CACHE_BYTES`.

## Develop

```bash
go vet ./... && go test ./...
docker build -t orchestra/registry:latest .
```
