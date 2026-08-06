# Orchestra Agent Runtime

The **agent** is the program that runs *inside* an Orchestra Docker sandbox. It
is the container entrypoint (working dir `/work`, the mounted git worktree). It
reads a task prompt, runs a tool-use loop against Claude's Messages API, edits
files in `/work` through scoped tools, and emits A2A-style structured JSON log
lines to stdout (captured by `docker logs`). It exits `0` when Claude finishes
its turn.

Stdlib-only Go module (`orchestra/agent`, `go 1.25`) — it builds offline.

## Credential-free by design

The agent **never holds an API key**. Every call to Claude goes to
`{ANTHROPIC_BASE_URL}/v1/messages`, which is the Orchestra security gateway's
Anthropic prefix (default `http://host.docker.internal:8787/anthropic`). The
gateway strips the `/anthropic` prefix, forwards to `api.anthropic.com`, and
injects the `x-api-key` and `anthropic-version` headers on the way out. The
agent therefore sends **only** `Content-Type: application/json` — it sets no
auth or version headers.

## Environment variables

| Variable             | Default                                            | Purpose |
| -------------------- | -------------------------------------------------- | ------- |
| `ANTHROPIC_BASE_URL` | `http://host.docker.internal:8787/anthropic`       | Gateway Anthropic prefix (no `/v1/messages`). |
| `ORCHESTRA_MODEL`    | `claude-opus-4-8`                                  | Model id. |
| `ORCHESTRA_SYSTEM`   | short built-in coding-agent prompt                 | System prompt. |
| `ORCHESTRA_TASK`     | —                                                  | Task prompt. If set, it wins over the task file. |
| `ORCHESTRA_WORKDIR`  | `/work`                                            | Worktree root. |

If `ORCHESTRA_TASK` is unset, the task is read from
`/work/.orchestra/task.md`.

## Tools exposed to Claude

All tools are scoped to `/work` with path-traversal protection: a path that is
absolute or escapes the root (`..`) is rejected before any filesystem access.

| Tool         | Input                     | Effect |
| ------------ | ------------------------- | ------ |
| `list_files` | `subdir?`                 | Newline list of files under `/work` (skips `.git`). |
| `read_file`  | `path`                    | File contents. |
| `write_file` | `path`, `content`         | Create/overwrite within `/work` (makes parent dirs). |
| `edit_file`  | `path`, `old_str`, `new_str` | Exact single-occurrence string replace (errors on 0 or >1 matches). |

## The tool-use loop

1. Build the request: `model`, `max_tokens: 16000`, `system`,
   `thinking: {type:"adaptive"}` (adaptive is correct for opus-4-8 — no
   `budget_tokens`, no `temperature`/`top_p`), the message history, and the tool
   definitions.
2. POST it and read `stop_reason`:
   - **`tool_use`** — append the full `response.content` array verbatim as an
     assistant message (thinking and tool_use blocks preserved byte-for-byte),
     execute every `tool_use` block, then append one user message carrying a
     `tool_result` block per call (matching `tool_use_id`), and loop.
   - **`end_turn`** — done; exit `0`.
   - **`max_tokens`** (or any other stop reason) — log and stop.
3. The loop is capped at 40 iterations to avoid runaways.

Unknown content-block types (notably `thinking`) round-trip verbatim via a
custom (un)marshaller, so echoed assistant turns are exactly what the API sent —
which the Messages API requires for thinking/tool_use continuity.

Each turn emits one JSON log line (role, stop reason, tool calls, and
`response.usage` token counts) to stdout.

## Develop

```sh
go build ./...
go vet ./...
go test ./...
```

The loop is tested end-to-end against a mock Messages API (`httptest`): the
first response calls `write_file`, the second returns `end_turn`, and the test
asserts the file was written and the loop terminated.

## Build the image

```sh
docker build -t orchestra/agent:latest .
```

Multi-stage: `golang:1.25` builds a static `CGO_ENABLED=0` binary, copied onto
`gcr.io/distroless/static` with `WORKDIR /work` and `ENTRYPOINT ["/agent"]`.
