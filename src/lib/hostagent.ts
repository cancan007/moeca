// Client for the Orchestra host agent (git worktree + diff + CI + merge).
// The service runs on loopback as a Tauri sidecar; in dev it is started with
// `cd hostagent && go run . -config config.json`.

import type { Artifact } from "@/lib/daily";

const BASE = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";

export interface LiveTask {
  id: string;
  repo: string;
  branch: string;
  target: string;
  worktreePath: string;
  additions: number;
  deletions: number;
  files: number;
  ci: "none" | "running" | "passed" | "failed";
}

export interface DiffLine {
  type: "hunk" | "add" | "del" | "context";
  content: string;
  oldNo?: number;
  newNo?: number;
}

export interface DiffFile {
  path: string;
  additions: number;
  deletions: number;
  lines: DiffLine[];
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export const hostagent = {
  async health(signal?: AbortSignal): Promise<boolean> {
    try {
      const res = await fetch(BASE + "/health", { signal });
      return res.ok;
    } catch {
      return false;
    }
  },
  tasks: () => req<{ tasks: LiveTask[] }>("/tasks").then((r) => r.tasks),
  // Older hostagents send null for an empty diff and for the .lines of a binary
  // or rename-only file; the review panes map over both in render.
  diff: (repo: string, branch: string) =>
    req<{ files: DiffFile[] }>(`/task/diff?repo=${encodeURIComponent(repo)}&branch=${encodeURIComponent(branch)}`)
      .then((r) => (r.files ?? []).map((f) => ({ ...f, lines: f.lines ?? [] }))),
  file: (repo: string, branch: string, path: string) =>
    req<{ content: string }>(`/task/file?repo=${encodeURIComponent(repo)}&branch=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}`).then((r) => r.content),
  files: (repo: string, branch: string) =>
    req<{ files: string[] }>(`/task/files?repo=${encodeURIComponent(repo)}&branch=${encodeURIComponent(branch)}`).then((r) => r.files),
  writeFile: (repo: string, branch: string, path: string, content: string) =>
    req<{ saved: string }>("/task/file", { method: "POST", body: JSON.stringify({ repo, branch, path, content }) }),
  ci: (repo: string, branch: string) =>
    req<{ status: string; output: string }>("/task/ci", { method: "POST", body: JSON.stringify({ repo, branch }) }),
  merge: (repo: string, branch: string) =>
    req<{ merged: string; into: string }>("/task/merge", { method: "POST", body: JSON.stringify({ repo, branch }) }),
  /** Push the task branch and open a pull request. Separate from merge: that one
   *  lands the branch locally and pushes nothing. Idempotent — an already-open
   *  PR is returned with created=false. */
  createPR: (repo: string, branch: string, title?: string, body?: string) =>
    req<PullRequest>("/task/pr", { method: "POST", body: JSON.stringify({ repo, branch, title, body }) }),
  /** What the task's worktree holds as output, classified for review.
   *
   *  A diff answers "what changed"; it cannot answer "what does the generated
   *  image look like" — it says "binary file changed". This is the same listing
   *  Daily's gallery uses, pointed at a worktree. */
  artifacts: (repo: string, branch: string) =>
    req<{ artifacts: Artifact[] }>(`/task/artifacts?repo=${encodeURIComponent(repo)}&branch=${encodeURIComponent(branch)}`).then((r) => r.artifacts),
  /** URL of one artifact's bytes, used directly as an <img>/<audio>/<video> src.
   *  Only media is served inline; anything else comes back as a download
   *  whatever this flag says. */
  artifactUrl: (repo: string, branch: string, path: string, download = false) =>
    `${BASE}/task/artifact?repo=${encodeURIComponent(repo)}&branch=${encodeURIComponent(branch)}&path=${encodeURIComponent(path)}${download ? "&download=1" : ""}`,
};

export interface PullRequest {
  url: string;
  number: number;
  branch: string;
  base: string;
  created: boolean;
}
