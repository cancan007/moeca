# Orchestra — Security Architecture

This document explains **how Orchestra isolates untrusted agents from the host and
the internet**, and why the design holds even when an agent (or the model driving
it) is fully compromised. It is written to be read layer by layer: network (L3),
ports (L4), the gateway application (L7), and the filesystem-based delegation
channel.

The single invariant everything below serves:

> **A strict sandbox can reach exactly two things — the gateway and the package
> registry proxy. Never the host, never the internet, never another sandbox.**

Both are host-owned L7 proxies with fixed upstreams, and both exist for the same
reason: a sandbox with a real route out could not be reasoned about, so anything
it legitimately needs from the outside is served by a named neighbour on its own
island instead. The gateway (§4) covers model and tool traffic; the registry
proxy (§8) covers dependency installs, which would otherwise be the one thing
that forces a hole in the boundary.

Agents hold **no API keys**; the gateway injects them. Agents open **no network
path to the host**; even privileged operations (spawning sub-agents) go through a
shared file, not a socket.

---

## 1. Components and where they run

| Component | Runs as | Network | Holds keys? |
|---|---|---|---|
| Tauri shell + React UI | Host process | Host | Raw admin token, seeds keychain |
| `gateway/` :8787 | **Docker container** | `orchestra-egress` **and** `orchestra-upstream` (dual-homed) | **Yes** — the only key holder |
| `sandbox/` :8789 | Host process | Host (loopback) | No |
| `hostagent/` :8788 | Host process | Host (loopback) | No |
| `ragindex/` :8790 | Docker container | `orchestra-upstream` only | No |
| `registry/` :8791 | **Docker container** | `orchestra-egress` **and** `orchestra-upstream` (dual-homed) | No |
| agent runtime | Docker container (per stage/sub-agent) | `orchestra-egress` only (strict), or **none** | **No** |

The Tauri shell provisions the Docker networks and spawns the sidecars; it holds
the raw admin token and seeds provider keys into the gateway over a loopback admin
API. See [`src-tauri/src/sidecars.rs`](../src-tauri/src/sidecars.rs).

---

## 2. Layer 3 — the network topology is the primary isolation

Egress is **not** enforced by a per-destination allowlist/firewall. It is enforced
by **routing**: a strict sandbox has no route to anything except the members of its
own network.

### 2.1 Two networks

- `orchestra-egress` is created with **`--internal`** — Docker attaches **no route
  to the host or the internet** to it. A container whose only interface is on this
  network is an island: there is simply nowhere to send a packet except other
  members of the same network.
- `orchestra-upstream` is an **ordinary bridge** (not internal) — it has NAT to the
  outside.

See [`src-tauri/src/sidecars.rs`](../src-tauri/src/sidecars.rs):
`EGRESS_NETWORK` (`--internal`, L36) and `UPSTREAM_NETWORK` (bridge, L37);
`ensure_network(EGRESS_NETWORK, true)` / `ensure_network(UPSTREAM_NETWORK, false)`.

### 2.2 The gateway is multi-homed (a NIC per network)

A host is not intrinsically bound to one network. **Joining a network = getting one
network interface (NIC) with one IP on it.** The normal case is 1 host → 1 network →
1 NIC. The gateway is the exception: it is attached to **two** networks, so it has
**two NICs**, one per network:

```
gateway container interfaces (illustrative)
  eth0  172.20.0.2   <- orchestra-egress  (sandboxes reach it here)
  eth1  172.21.0.2   <- orchestra-upstream (internet-capable, NAT)
```

This is classic **multi-homing** — exactly what a router / firewall / bastion does:
one host straddling two segments. It is *not* "two NICs on one network"; each NIC
belongs to a different network. In [`sidecars.rs`](../src-tauri/src/sidecars.rs) the
gateway is started `--network orchestra-upstream` (its **primary** network) and
then given its second interface with `docker network connect orchestra-egress
<gateway>`. The order matters at L4: the primary network must be the non-internal
`orchestra-upstream`, because Docker does **not** activate a published port
(`-p 127.0.0.1:8787`, §3) for a container whose primary network is `--internal`.
Putting the internal NIC second keeps the host↔gateway door working while leaving
isolation unchanged — a sandbox's reachability depends on *its own* egress-only
`--internal` attachment (§2.3) and the gateway's L7 SSRF-deny (§4.3), not on which
network the gateway booted on.

```
      orchestra-egress (--internal)          orchestra-upstream (bridge + NAT)
            [eth0]                                   [eth1]
              \                                       /
               \        +--------------+             /
   sandbox -----+--L3-->|   gateway    |<---L3------+-------> api.anthropic.com
   (egress only)   |    | (keys+authz) |                     api.github.com
                   |    +------+-------+
                   |           | L4: publishes 127.0.0.1:8787 only
                   |           v
                   |      host UI
                   |
                   |    +--------------+
                   +--->|   registry   |<---L3--------------> registry.npmjs.org
                        | (GET/HEAD,   |                      pypi.org
                        |  fixed hosts)|                      proxy.golang.org
                        +--------------+                      crates.io
```

The registry proxy (§8) is the **only** other member of the island. It is the same
multi-homing shape as the gateway, applied to dependency fetches; a sandbox still
has exactly two things it can reach, and neither of them is the host.

### 2.3 Why this beats an allowlist

If a sandbox is fully compromised it still cannot reach the host controller
(:8789), another sandbox, or the internet — **because no route exists**, not because
a rule denies it. There is no firewall to misconfigure. The gateway is reachable
only because it shares the `orchestra-egress` island; it can reach the internet only
because its *second* NIC sits on the NAT'd `orchestra-upstream` network.

### 2.4 Private addressing (RFC1918) is what makes many islands cheap

Multi-homing is why the gateway can *bridge* two segments; it is a general concept,
independent of address type. **Private addressing is a separate benefit:** each
Docker user-defined network gets its own bridge + subnet carved from RFC1918 ranges
(e.g. `172.16.0.0/12`). Because private ranges need no global uniqueness, the host
can spin up arbitrarily many mutually-isolated segments (egress, upstream, RAG, …)
for free. This L3 fact also underpins the L7 SSRF defense (§4.3): "resolves to a
private IP" means "reachable via an inner interface" means "block it."

---

## 3. Layer 4 — the only host↔gateway door is a published port

The host UI must reach the gateway, but nothing else on the host should. This is
handled separately from L3, at the **port** level:

- The gateway container publishes **only** `127.0.0.1:8787:8787`
  ([`sidecars.rs`](../src-tauri/src/sidecars.rs), `publish = "127.0.0.1:8787:8787"`).
- Bound to loopback, so it is not exposed on the host's LAN interfaces.

Keep the two straight: **§2 (L3) is "which networks are wired together"; this (L4)
is "which single TCP port the host may knock on."** The gateway's *outbound* path to
the internet is the L3 second-NIC of §2.2 — **not** this port.

---

## 4. Layer 7 — the gateway decides where it will actually forward

L3 forces every agent request through the gateway. The gateway then governs the
*destination* in the application. The crossing between networks is an **L7 proxy
hop**, not transparent IP forwarding: the sandbox makes an explicit HTTP request
*to* the gateway, and the gateway issues a **new** outbound request on its upstream
NIC. That indirection is exactly what lets it enforce the checks below.

Each request passes: session auth → routing → body-size limit → rate limit →
token/cost budget → allowlist → credential injection → streaming proxy → structured
access log. See [`gateway/internal/gateway/gateway.go`](../gateway/internal/gateway/gateway.go).

### 4.1 Key injection — agents never hold secrets

Agents set only `Content-Type`; the gateway injects `x-api-key` (Anthropic) /
bearer token (GitHub) from its own environment / keychain-seeded memory. Keys never
appear in the container, in `docker inspect`, in localStorage, or in logs.

### 4.2 Allowlist (positive) + write-authz

- Fixed routes `/anthropic`, `/github` go only to configured upstreams; `/fetch`
  dynamic targets must be on the allowlist.
- GitHub mutations are **deny-by-default**: only branch-create / PR-create /
  comments pass. **PR merge, pushes to protected branches (`main`/`master`/
  `develop`), and direct `contents` commits are rejected** even with a valid token
  (opt back in via `writeAllow`). Landing changes onto a base branch stays a host
  action.

### 4.3 SSRF / host-reach deny (negative)

See [`gateway/internal/gateway/admin.go`](../gateway/internal/gateway/admin.go)
`targetBlocked`:

- Docker-host aliases (`host.docker.internal`, `gateway.docker.internal`,
  `host.lima.internal`, `host.orb.internal`, `kubernetes.docker.internal`) are
  **always** blocked (string match).
- IP literals in loopback / private / link-local / unspecified ranges are blocked
  (`blockedIP`).
- **Dynamic (caller-supplied) targets are DNS-resolved and blocked if any resolved
  IP is private** — a DNS-rebinding defense. Fixed, admin-set upstream hostnames
  are trusted and **not** re-resolved.

### 4.4 Admin API is loopback + hashed token

Providers/secrets are managed via a loopback admin API. The gateway holds only the
**SHA-256 hash** of the admin token (the raw token lives host-side, in the Tauri
shell); tokens are compared in constant time (`adminAuthed`). `GET` never returns a
secret (exposes only `hasSecret`). Sandboxes never receive the admin token.

---

## 5. Sandbox hardening (per agent container)

Built purely in [`sandbox/internal/docker/docker.go`](../sandbox/internal/docker/docker.go)
`RunArgs` (a pure function, so the hardening surface is unit-testable):

- `-v <worktree>:/work:rw` — **only** the task's worktree is mounted; nothing else
  from the host.
- `--read-only` root fs + `--tmpfs /tmp` writable scratch, plus any extra tmpfs
  paths the image's policy declares (§5.2).
- `--cap-drop ALL`, `--security-opt no-new-privileges`.
- `--network orchestra-egress` (strict; the `--internal` island of §2), or
  `--network none` when the policy asks for it (§5.1).
- `--pids-limit`, `--memory`, `--cpus` resource caps.
- Host process environment is **never** read; only explicitly-allowed, non-secret
  env vars are passed.

A `relaxed` mode (ordinary bridge, for future interactive terminals) exists, but the
controller derives the gateway base URLs from the mode **server-side**, so a client
cannot downgrade egress.

### 5.1 The image allowlist — the image is data, the hardening is code

A stage does not send a container image. It names a **policy** from a host-owned
allowlist ([`sandbox/internal/api/images.go`](../sandbox/internal/api/images.go)),
and the controller supplies everything else: the image reference, the network
posture, the resource caps, the scratch mounts. Nothing a caller supplies reaches
`docker.RunArgs`, whose flag vector is **identical for every image** — asserted
directly in `TestRunArgs_HardeningIsIndependentOfTheImage`.

That inversion is what makes bring-your-own-image a *supply-chain* question
rather than an isolation one. A hostile image still runs read-only, without
capabilities, with only the task's worktree mounted and no route off the island:
it can burn CPU and corrupt `/work`, and nothing else. What the allowlist adds is
accountability — the reference is resolved **host-side to an immutable digest**
before launch (`docker.Resolve`), and both the policy name and that digest are
recorded on the stage. A tag moves; the digest is what makes "which bytes
actually ran" answerable a month later.

Three images ship:

| Policy | Contents | Posture |
| --- | --- | --- |
| `base` | distroless, agent binary only — no shell, no toolchain | egress |
| `poly` | Node 22 / Python 3 / Go 1.25 + shell, for build/test/debug **command** stages | egress |
| `media` | ffmpeg / ImageMagick / libvips | **`--network none`** |

`media` is separate on purpose. Media parsers consume untrusted, attacker-shaped
binary input and carry a decade of CVEs; putting them in the common image would
widen *every* agent's attack surface to cover all of it. Isolated as their own
stage they also need no network at all — a transcode has nothing to say to the
gateway — so that stage ends up **more** confined than a normal sandbox, not less.
A policy may only ever be more restrictive than the run's isolation; `network`
accepts `egress` or `none` and nothing else.

`Stage.Cmd` is what makes `poly` and `media` usable: it replaces the image's
default command, so a stage can run `npm ci && npm test` or `ffmpeg …` instead of
an LLM loop. The command runs under the same hardening as everything else — it
changes what a stage *does*, not what it can reach. Note that this is a
**stage-level** capability authored host-side, not a shell handed to the model:
the agent's own tool set stays file operations plus gateway-routed HTTP.

### 5.2 Attended vs unattended, not Delivery vs Daily

The second axis on a policy is `unattended`, and it splits images by whether a
human is at the wheel when the run starts:

- **Daily** fires on a schedule with nobody watching → only policies marked
  `unattended` are admitted.
- **Delivery** is attended (a reviewer is in the review drawer) → any policy runs.

Framing it as attended/unattended rather than by screen is what keeps it correct
as screens are added, and it produces the rule that matters: an image someone
adds while debugging is usable in Delivery immediately, but reaching a Daily
schedule takes an explicit promotion in Settings. Enforcement is at **admission** —
every stage's image is resolved before the run is created, so a bad or unpromoted
image is a 400, not a run that dies at stage 7 having produced half a deliverable.
The host agent **forces** the flag when it fires a schedule rather than trusting
the stored spec, so a schedule compiled before the flag existed is still
restricted.

### 5.3 Writable scratch is tmpfs, never a shared volume

A read-only root and a real toolchain collide: npm wants a cache, pip wants a
cache, go wants a module cache. Each image policy declares the paths it needs and
they are mounted `--tmpfs` — RAM-backed, gone with the container.

The tempting fix is a named volume shared between tasks, and it is the wrong one:
a writable cache mount lets one task plant bytes that another task later
executes, which is precisely the cross-task contamination the one-worktree-per-task
rule exists to prevent (§6.4). The durable cache lives in the registry proxy
instead (§8.3), where a sandbox can only ever **read** it, over HTTP.

Process lifecycle: sidecars are reaped on app quit (`RunEvent::Exit`) **and**
self-terminate via a parent-PID watchdog if the app is `SIGKILL`ed.

---

## 6. Runtime delegation without piercing the boundary

A supervisor agent can spawn sub-agents — but spawning a container is a host
privilege, and letting the sandbox call the host controller over the network would
punch a hole in §2. Instead delegation uses a **file-based worktree channel**: no
socket, no route to the host.

### 6.1 One directory, two path names

The worktree is bind-mounted into the container, so the *same* directory is seen as:

- `/work/.orchestra/delegate/<id>/` inside the sandbox
  ([`agent/internal/tools/delegate.go`](../agent/internal/tools/delegate.go))
- `<worktreeDir>/.orchestra/delegate/` on the host
  ([`sandbox/internal/api/delegate.go`](../sandbox/internal/api/delegate.go))

### 6.2 Host-initiated polling (direction matters)

```
sandbox agent (egress only)                 host controller (can run docker)
       |                                              |
  spawn_subagent tool                                 |
       | write request.json (tmp -> rename, atomic) ->| polls worktree every 500ms
       |                                              | sees request.json, runs the
       |                                              | sub-agent as a hardened
       |                                              | container in the SAME worktree
       |<-- poll until result.json appears -----------| writes result.json (tmp->rename)
       | return summary                               |
```

The sandbox **opens no connection** — it only writes/reads files on its own mount.
The **host** actively polls the directory it owns. Because the initiative is always
host-side (host → its own filesystem, never sandbox → host), a compromised container
has no socket to the controller to abuse — it cannot reach it (§2). Atomic
`request.json.tmp → rename` guarantees the watcher never reads a partial request.

### 6.3 Depth cap

Stage agents = depth 0 (may delegate); sub-agents = depth 1 (may not), via
`ORCHESTRA_DELEGATE_DEPTH` / `ORCHESTRA_DELEGATE_MAX`, so delegation cannot recurse
without bound.

### 6.4 The trade-off to remember

This design moves the trust boundary from **network reachability** to **worktree
write access**: whoever can write the worktree can trigger a delegation. That is
safe *because each worktree is isolated to one task*. **If a future change ever
shares a worktree across tasks, this assumption breaks** — any sandbox→host control
path must reuse the no-network worktree channel (or another routeless mechanism),
never a gateway exception to reach `host.docker.internal`.

---

## 7. RAG stays on the far side of the gateway too

The RAG indexer container sits on `orchestra-upstream` (gateway-reachable) but
**not** on `orchestra-egress`, and publishes only a loopback management port. A
sandboxed agent therefore reaches search **only via the gateway's `/rag` route** —
never the RAG service or the host directly. The indexer mounts the knowledge folder
**read-only** and fetches embeddings **through the gateway** (it holds no key).

Which folders it may read is registered in Settings and held by the Tauri shell
([`src-tauri/src/knowledge.rs`](../src-tauri/src/knowledge.rs)), because mounting
is a host action. Each folder is bind-mounted **read-only into the indexer and
nowhere else** — never into a sandbox — so registering one grants *retrieval*
through the gateway, not file access. A bind mount cannot be added to a running
container, so changing the set restarts the indexer.

That restart is cheap for the index itself — sources are the truth and rebuilding
from them is always correct — but the container does hold three things across it,
in a host volume: the embedded vectors (so a rebuild pays the provider only for
text it has never seen), the image captions (same reason, a model call each), and
the **group membership pushed from the host**. The last one is not derived state:
the indexer cannot read the Knowledge graph — it is off that network, by the same
design as everything else here — so if it forgets, it cannot find out again.
Losing that directory is therefore a permissions event, not just a bill.

### 7.1 Reachability is not entitlement

Being able to call `/rag/search` says nothing about what it returns. A second
layer decides that, and the two are deliberately separate.

A task names a **node of the Knowledge graph** — an organization or a project —
rather than a list of groups, because the groups under a node change as the graph
is edited and a task that meant "this project's knowledge" should follow it. The
host resolves that node to a group set **at launch**, the sandbox controller mints
a gateway session carrying it, and the gateway states it to the indexer as a
header it will not accept from anyone. The indexer filters chunks by it.

Three properties hold that together:

- **The caller never names its own entitlement.** The gateway discards any
  inbound `X-Orchestra-Groups` and injects the session's own. A sandbox that
  could name its groups could name all of them.
- **A scope is absolute.** Nothing downstream widens it. Relations between groups
  are documentation and grant nothing — they briefly did, bounded by a hop count
  on the agent template, and the bound was the wrong kind of safety: a setting
  owned by whoever wrote the agent could enlarge a boundary set by whoever wrote
  the task. Widening is done where it can be seen, by putting a group in the
  project.
- **No scope means no retrieval, not all of it.** A session that never stated an
  entitlement is refused (`403`), because the default a task is created with must
  not also be the widest grant. Reaching the knowledge declared as everyone's is
  the *global* scope, chosen explicitly, and it resolves to an empty group list —
  which is why nil and empty are kept distinct the whole way down.

What a run was entitled to is recorded in the access log beside what it actually
retrieved. Reconstructing the grant afterwards would answer against the graph as
it is *now*, and an edge drawn since would make a past run look wrong.

---

## 8. Dependencies — the registry proxy, the second egress path

A sandbox that can only reach the gateway cannot run `npm install`, `pip install`
or `go mod download`. That is the one requirement that genuinely collides with
§2, and the tempting resolutions are both bad: dropping the stage onto a bridge
network abandons the invariant outright, and allowing the gateway to fetch
arbitrary hosts turns a fixed-upstream proxy into a general-purpose fetcher.

The resolution is to apply the §2.2 shape a second time. The registry proxy
([`registry/`](../registry/)) is dual-homed on `orchestra-egress` and
`orchestra-upstream`, so a package manager inside a sandbox resolves to a
container it *can* reach, which then issues a **new** request upstream on its own
NIC. The island is unchanged: a sandbox still has no route to the internet, and
its reachable set grows by exactly one named neighbour.

### 8.1 Why this is a security gain, not a hole

| Property | Consequence |
| --- | --- |
| **GET / HEAD only** — enforced first and unconditionally in the handler | There is no code path that writes to a registry. A compromised agent cannot publish a package, overwrite a version, or exfiltrate through a publish endpoint — the classic supply-chain egress. |
| **No caller-supplied destinations** | Upstreams are fixed per ecosystem in host-owned config; a request path selects an *ecosystem*, never a host. Unlike the gateway's `/fetch` (§4.2) there is no dynamic-target surface to defend at all. |
| **Bounded redirects** | Registries redirect downloads to CDNs; a redirect is followed only to that ecosystem's declared hosts. An upstream cannot walk the proxy onto an arbitrary origin, and private-IP targets are excluded because the allowlist is positive. |
| **No credentials** | Public registries only. Inbound `Authorization` / `Cookie` are dropped rather than relayed, and no token variable exists to steal. |
| **Every fetch logged** | Dependency downloads are where supply-chain attacks actually land. They now pass one chokepoint and are recorded there — the same argument as §9.1, applied to code rather than prompts. |

The endpoints are forced **server-side** by the controller
(`NPM_CONFIG_REGISTRY` / `PIP_INDEX_URL` / `GOPROXY` / `CARGO_REGISTRIES_*`),
exactly like the gateway base URLs and for a stronger reason: redirecting an
install decides which code ends up executing. Module checksum verification is
deliberately left **on** — the go sum database is fetched through the same proxy,
and nothing sets `GOSUMDB=off`.

### 8.2 URL rewriting is what makes it work

Registry metadata embeds **absolute** download URLs — npm's `dist.tarball`, the
PyPI simple index's links to `files.pythonhosted.org`, cargo's `dl`. A sandbox
that followed one verbatim would be dialling a host it has no route to, and the
install would hang rather than fail cleanly. Every such URL is rewritten to point
back at the proxy before the body is served; a unit test asserts that each
rewrite target is a prefix the proxy actually serves.

### 8.3 The durable cache lives here, on purpose

Sandbox scratch is tmpfs (§5.3), so without a cache every run re-downloads its
whole dependency tree. The cache is held **by the proxy**, not in a volume shared
between sandboxes — a shared writable cache mount would let one task plant bytes
another task later executes. Here a sandbox can only *read* it, over HTTP,
through the same GET-only surface as everything else.

Only **immutable** artifacts are cached — tarballs, wheels, module zips, selected
by path markers chosen so no version listing can match — so a hit is always
correct and an install is never pinned to a stale view of a registry. The budget
is bounded with oldest-first eviction.

---

## 9. Audit — the isolation chokepoint is also the observability plane

The same boundary that isolates agents (§2–§4) gives observability for free, with
a stronger trust model than ordinary agent logging.

### 9.1 Why capture at the gateway

An agent could log its own actions — but a compromised agent can lie. Because a
strict sandbox can reach **only** the gateway, **every** LLM call, tool call, and
response necessarily passes through that one chokepoint and is recorded there. The
record is produced by the host-trusted gateway, not the agent, so it is complete
and tamper-evident: the agent cannot omit a call it made, because it could not have
made the call without going through the recorder.

### 9.2 What is captured

Each request becomes one structured `accessLog`
([`gateway/internal/gateway/logging.go`](../gateway/internal/gateway/logging.go)),
held in an in-memory ring buffer and written as JSON lines:

- attribution: `run` / `stage` ids, `service`, `model`, `session`, `requestId`
- outcome: `status`, `durationMs`, byte counts, `err`
- **content**: `reqBody` (the actual prompt) and `respBody` (the actual model
  output), each capped at `maxCaptureBytes` (32 KiB — sized to hold a full
  multi-block response: thinking + tool_use + text)
- **real token usage**: `inputTokens` / `outputTokens` parsed from the provider's
  usage frame (a rolling response *tail* is kept for this, separate from the
  content prefix), plus `tokensEst` charged to the budget

### 9.3 Captured content is itself admin-gated

Prompts and responses are sensitive, so the logs/metrics endpoints are **admin-
gated**: the UI reads them through Tauri commands
([`src-tauri/src/providers.rs`](../src-tauri/src/providers.rs)) using the admin
token that lives only host-side. A sandbox (which holds only a session token)
cannot read them, and a plain browser with no desktop shell gets nothing. The
audit content is protected by the same admin-token mechanism as provider secrets
(§4.4).

### 9.4 Semantic reconstruction (the A2A trace)

The raw capture is turned into a semantic tree for review. The agent is
non-streaming, so `respBody` is a single Messages JSON with a `content` block
list; [`src/features/audit/a2a.ts`](../src/features/audit/a2a.ts) parses it into:

```
Context (run) → Task (stage) → Message (one LLM call)
                                  → Part per block:
                                     input · thinking · tool_use · text
```

The three response layers are surfaced distinctly — **thinking** (what the agent
interpreted), **tool_use** (what it decided to do), **text** (what it output) — so
a reviewer sees the agent's reasoning apart from its actions and its answer. A
truncated capture is surfaced as a `raw` part rather than silently dropped.

This parsing is **presentation-side by design**: the gateway stays a thin
security-focused proxy (it captures raw bytes + parses only token usage); the
semantic reconstruction is a view concern in pure, unit-tested TS
(`a2a.ts`, `a2a.test.ts`).

### 9.5 The optimize loop (run diff)

[`src/features/audit/runDiff.ts`](../src/features/audit/runDiff.ts) summarizes each
run from the access log (tokens, LLM calls, tool round-trips, errors, wall time,
per-stage token split) and diffs two runs, so a change — a shorter prompt, context
compression, a different model, fewer tool round-trips — is **measured, not
eyeballed**. The verdict is driven by the headline cost metric (total tokens) and
downgraded to *mixed* when a token win comes with more errors. This closes the loop
from *observe* to *improve*.

## 10. Summary — defense in depth by layer

| Layer | Mechanism | Guarantee |
|---|---|---|
| L3 network | `--internal` egress island; gateway and registry proxy dual-homed | Sandbox has **no route** to host/internet/peers; only those two neighbours |
| L4 port | gateway publishes only `127.0.0.1:8787` | Host↔gateway is the one loopback door |
| L7 gateway | allowlist + write-authz + SSRF-deny + key injection | Controls *which* URL is reached; agents hold no keys |
| L7 registry | GET/HEAD only, fixed upstreams, bounded redirects, no credentials | Dependencies can be fetched but never published; no dynamic-target surface |
| Image policy | host-owned allowlist, digest pinned at launch, unattended gate | A stage picks *which* image, never *how* it runs; the run records the bytes that executed |
| Container | read-only, cap-drop ALL, no-new-privileges, worktree-only mount, tmpfs scratch, resource caps, optional `--network none` | Compromise is contained to `/work` |
| Delegation | file-based worktree channel, host-initiated polling | Privileged spawn without any sandbox→host socket |
| Audit | capture at the single chokepoint, admin-gated content | Complete, tamper-evident record of every agent I/O — the agent cannot log around it |

Every layer assumes the one above it may fail. A compromised model, a compromised
agent process, and a compromised container each stop at the next boundary.

The audit plane is a corollary of the isolation design, not a bolt-on: because
every path out of a sandbox is a proxy Orchestra owns, those proxies are
simultaneously the security boundary (§2–§4, §8) and the complete observability
point (§9) — model traffic at the gateway, dependency fetches at the registry.
