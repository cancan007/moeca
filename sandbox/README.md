# sandbox

Orchestra's **sandbox control plane**. It launches and manages hardened Docker
containers for agents. Each sandbox can touch **only** its task's git worktree
and holds **no secrets** — every outbound API call goes through the separate Go
**gateway**, which injects credentials, so the container never carries keys or
sees the host environment.

It runs as a Tauri sidecar bound to loopback, next to the gateway (`:8787`) and
host agent (`:8788`). Default listen: `127.0.0.1:8789`.

## Isolation

Every container is started via `docker run` with this hardening (built by the
pure, unit-tested `docker.RunArgs`):

| Flag | Purpose |
| --- | --- |
| `--label orchestra=1` / `--label orchestra.task=<taskId>` | Identify managed containers and their task. |
| `-v <worktreePath>:/work:rw` + `-w /work` | Mount **only** the task worktree; nothing else from the host. |
| `--read-only` + `--tmpfs /tmp` (+ the image policy's extra tmpfs paths) | Immutable root filesystem with writable scratch. Toolchain caches are tmpfs, so they die with the container. |
| `--network orchestra-egress` (or `none`) | Dedicated egress network, not the default bridge. An image policy may narrow this to no network at all. |
| `--env KEY=VALUE` (allowed vars only) | Non-secret vars such as gateway base URLs. The host process environment is **never** passed through. |
| `--cap-drop ALL` | Drop every Linux capability. |
| `--security-opt no-new-privileges` | Block privilege escalation. |
| `--pids-limit`, `--memory`, `--cpus` | Process and resource caps. |
| `--name orchestra-<taskId>` | Deterministic container name. |

**This flag vector does not vary with the image.** A stage names a *policy* from
the allowlist below; it never sends an image reference, a network, a resource cap
or a mount, so nothing a caller supplies reaches `RunArgs`. That is asserted
directly by `TestRunArgs_HardeningIsIndependentOfTheImage`.

## Image allowlist

A stage's `image` field is a **policy name**, not a reference. The controller
looks up the reference, the network posture, the resource caps and the writable
scratch paths, then resolves the reference **host-side to an immutable digest**
before launch — a tag moves, so the digest is what makes "which bytes ran"
answerable afterwards. Both the policy name and the digest are recorded on the
stage.

| Policy | Purpose | Posture |
| --- | --- | --- |
| `base` | agent stages (distroless, no shell) | egress |
| `poly` | build / test / debug **command** stages (`stage.cmd`), Node·Python·Go | egress |
| `media` | ffmpeg / ImageMagick over untrusted input | `--network none` |

A policy may only ever be **more** restrictive than the run's isolation:
`network` accepts `egress` or `none` and nothing else, and resource limits are
clamped to the configured ceilings.

`unattended` is the second axis. Daily fires on a schedule with nobody watching,
so it is limited to policies marked `unattended`; Delivery runs are attended and
may use any of them. A custom image added from Settings is therefore usable in
Delivery immediately but needs an explicit promotion to reach a schedule. The
check runs at **admission** — every stage's image is resolved before the run is
created, so a bad or unpromoted image is a `400`, not a run that dies halfway.

## Dependency installs

Strict sandboxes have no route to the internet, so package managers are pointed
at the [registry proxy](../registry/) on the egress network. The controller
forces `NPM_CONFIG_REGISTRY` / `PIP_INDEX_URL` / `GOPROXY` / `CARGO_REGISTRIES_*`
server-side, exactly like the gateway base URLs and for a stronger reason:
redirecting an install decides which code ends up executing. Nothing disables
module checksum verification.

## API

Loopback HTTP, JSON in/out.

| Method & path | Body / query | Result |
| --- | --- | --- |
| `GET /health` | — | `{"status":"ok"}` |
| `POST /sandbox` | `{taskId, worktreePath, image?, cmd?, env?}` | create + start → `{"id"}` |
| `GET /sandboxes` | — | list orchestra containers → `{"sandboxes":[…]}` |
| `GET /sandbox/logs?id=` | `id` query | `{"id","logs"}` |
| `POST /sandbox/stop` | `{id}` | `{"stopped"}` |
| `DELETE /sandbox?id=` | `id` query | `{"removed"}` |
| `GET /images` | — | the allowlist → `{"images":[…],"default","maxMemoryMB",…}` |
| `POST /images` | an `ImagePolicy` | upsert a **custom** policy (built-in names are refused); `unattended:true` is the promotion → `{"images":[…]}` |
| `DELETE /images?name=` | `name` query | remove a custom policy → `{"images":[…]}` |

## Config

`config.json` sets the listen address, the image allowlist, and per-sandbox
defaults (network, resource caps, non-secret env such as the gateway base URLs):

```json
{
  "listen": "127.0.0.1:8789",
  "images": [
    { "name": "base",  "ref": "orchestra/agent:latest",       "unattended": true },
    { "name": "poly",  "ref": "orchestra/agent-poly:latest",  "unattended": true,
      "memoryMB": 4096, "cpus": 4, "tmpfs": ["/cache", "/root"] },
    { "name": "media", "ref": "orchestra/agent-media:latest", "unattended": true,
      "network": "none", "memoryMB": 6144, "tmpfs": ["/cache", "/root"] }
  ],
  "maxMemoryMB": 16384,
  "maxCPUs": 8,
  "egressNetwork": "orchestra-egress",
  "gatewayStrictBase": "http://orchestra-gateway:8787",
  "registryStrictBase": "http://orchestra-registry:8791",
  "memoryMB": 2048,
  "cpus": 2,
  "pidsLimit": 512,
  "env": { "ANTHROPIC_BASE_URL": "http://host.docker.internal:8787/anthropic" }
}
```

Omitting `images` falls back to the legacy single `image` field as the one
`base` policy, so a config that predates the allowlist keeps working. Custom
policies added from Settings are persisted separately (`images.json` in the log
directory) and never overwrite a shipped one.

## Run & test

```sh
go run . -config config.json     # start the service
go build ./... && go vet ./... && go test ./...
```

Tests are green **without a running Docker daemon**: `RunArgs` is a pure function
and the API handlers depend on a small `Client` interface stubbed by a fake in
tests.
