# Orchestra Host Agent

The host-side service that turns agent deliverables into reviewable git state.
Each task is a **git worktree**; the agent produces its change there, and the
host agent extracts the real diff against the target branch, runs the CI gate,
and merges on self-review approval. Runs as a Tauri sidecar (loopback only)
alongside the [security gateway](../gateway/).

## Why worktrees

`git worktree` lets many branches of the same repo be checked out at once, so
agents run in parallel in isolated working directories — the "同repoでもブランチ
ごとに並行" requirement — while review happens on the host.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | liveness |
| GET | `/tasks` | all worktree-backed tasks (branch, target, +/−, files, CI) |
| POST | `/task` | create a worktree `{repo, branch, base?}` |
| DELETE | `/task?repo=&branch=` | remove a worktree |
| GET | `/task/diff?repo=&branch=[&file=]` | **real** structured diff (hunk/line model the UI renders) |
| GET | `/task/file?repo=&branch=&path=` | worktree file content (the editable 原本) |
| POST | `/task/ci` | run the repo's `ciCommand` in the worktree → `passed`/`failed` |
| POST | `/task/merge` | merge branch → target — **gated**: requires CI passed (else `409`) |

The diff endpoint returns files as `{path, additions, deletions, lines[]}` where
each line is `{type: hunk|add|del|context, content, oldNo?, newNo?}` — the exact
shape the frontend review DiffPane consumes, so the board shows real diffs.

## Run

```bash
cd hostagent
go run . -config config.json   # config lists repos: {name, path, target, ciCommand}
```

## Test

```bash
go test ./...   # diff parser + full flow against a real temp git repo
                # (create worktree → change → diff → CI gate → merge)
```
