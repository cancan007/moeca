# Orchestra Gateway

The single **Go egress proxy** for sandboxed agents. Agents in isolated containers
make every outbound call through this gateway (bound to loopback on the host), so
they never hold API keys or reach upstreams directly.

```
one Go gateway
 ├── /anthropic/*   → api.anthropic.com   (x-api-key injected)
 ├── /github/*      → api.github.com      (bearer token injected)
 ├── /fetch/*       → allowlisted hosts   (dynamic target via X-Orchestra-Target)
 └── /registry/*    → internal registry
```

## Request pipeline

Every proxied request passes, in order:

1. **Session auth** — must present `X-Orchestra-Session: <token>` matching a configured session (anonymous allowed only when no sessions are configured).
2. **Routing** — longest matching service prefix.
3. **Body-size limit** — `maxBodyBytes` (default 8 MiB).
4. **Rate limit** — per-`(session, service)` token bucket (`rps` / `burst`).
5. **Token/cost budget** — per-`(session, service)` ceiling, estimated from bytes; returns `402` when exceeded.
6. **Allowlist** — resolved upstream host must be permitted (`.suffix` matches subdomains).
7. **Credential injection** — `injectHeaders` with `${ENV}` expansion; `stripHeaders` + the session/target/hop headers are removed before forwarding.
8. **Streaming proxy** — SSE / token streams flush immediately (`FlushInterval = -1`).
9. **Structured access log** — one JSON line per request (feeds the app's Audit view).

Control endpoints: `GET /_gateway/health` (no auth), `GET /_gateway/status` (auth — lists services + spent tokens).

## Run

```bash
cd gateway
export ANTHROPIC_API_KEY=...   # injected, never seen by agents
export GITHUB_TOKEN=...
go run . -config config.json
```

## Test

```bash
go test ./...        # session auth, injection/stripping, allowlist, rate limit,
                     # budget, streaming, body-size limit, config validation
```

Config is `config.json` (see the checked-in example). It runs as a Tauri sidecar
in production; today it runs standalone with `go run`.
