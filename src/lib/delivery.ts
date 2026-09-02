// Client for the host agent's Delivery GitHub-pull API: pull assigned GitHub
// issues (through the gateway, so no token touches the app) and promote one into
// a git worktree (branch ticket/<id>) to work it through the normal Delivery
// review/PR flow.

const BASE = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";

export interface Issue {
  id: string; // "github:owner/repo#123"
  source: string;
  title: string;
  body: string;
  url: string;
  state: string;
  repo: string; // "owner/repo"
  branch: string;
  labels: string[];
  updatedAt: string;
}

export interface RepoInfo {
  name: string;
  target: string; // default base branch
  /** Absolute repo path (present on the management view; the promote picker
   * only needs name+target). */
  path?: string;
  /** CI command (argv). Empty when no CI is configured. */
  ciCommand?: string[];
  /** true = store-managed (added via Settings, removable); false = config seed. */
  managed?: boolean;
  /** "owner/repo" from the repo's git origin — the GitHub repo this local repo
   * maps to (used to auto-route a pulled issue to its registered local repo). */
  githubSlug?: string;
}

/** Fields for adding/updating a store-managed repository. */
export interface RepoInput {
  name: string;
  path: string;
  target: string;
  ciCommand?: string[];
}

export interface Milestone {
  title: string;
  done: boolean;
}

import type { KnowledgeScope } from "@/lib/schedules";

export interface TaskMeta {
  goal: string;
  milestones: Milestone[];
  /** Assigned agent template ref — see lib/agentTemplates (normalizeRef also
   *  maps values stored before refs existed). */
  template: string;
  /** Knowledge this task's agents may retrieve, named as a node of the
   *  Knowledge graph. Absent means no scope, and such a run retrieves nothing:
   *  the gateway refuses a session that never stated an entitlement. */
  scope?: KnowledgeScope;
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

export const delivery = {
  /** Pull GitHub issues. With a registered repo name, pulls that repo's open
   * issues; without it, the caller's assigned issues across all repos. */
  pull: (repo?: string) =>
    req<{ pulled: number; issues: Issue[] }>(
      "/delivery/pull" + (repo ? `?repo=${encodeURIComponent(repo)}` : ""),
      { method: "POST" },
    ),
  /** Stored GitHub-sourced issues. */
  issues: () => req<{ issues: Issue[] }>("/delivery/issues").then((r) => r.issues),
  /** Configured local repos (name + default base branch, plus path/ciCommand/
   * managed for the Settings panel). */
  repos: () => req<{ repos: RepoInfo[] }>("/repos").then((r) => r.repos),
  /** Add/update a store-managed repository (persisted; card appears live). */
  addRepo: (r: RepoInput) =>
    req<{ added: string }>("/repos", { method: "POST", body: JSON.stringify(r) }),
  /** Remove a store-managed repository (config-file seeds cannot be removed). */
  removeRepo: (name: string) =>
    req<{ removed: string }>(`/repos?name=${encodeURIComponent(name)}`, { method: "DELETE" }),
  /** Promote an issue into a worktree (branch ticket/<id>) on the chosen repo/base. */
  promote: (id: string, repo: string, base?: string) =>
    req<{ ticket: string; repo: string; branch: string; worktreePath: string }>("/daily/promote", {
      method: "POST",
      body: JSON.stringify({ id, repo, base: base ?? "" }),
    }),
  /** Create a Delivery task locally: a new worktree/branch on the chosen repo,
   * no GitHub issue involved. base defaults to the repo's target branch. */
  createTask: (repo: string, branch: string, base?: string) =>
    req<{ repo: string; branch: string; worktreePath: string }>("/task", {
      method: "POST",
      body: JSON.stringify({ repo, branch, base: base ?? "" }),
    }),
  /** A Delivery task's goal + milestones + assigned template (by repo/branch). */
  /** Resolve a scope node to the groups it grants. The
   *  host owns the graph, so both screens ask it rather than each computing a
   *  different answer for "project X". */
  resolveScope: (scope: KnowledgeScope | undefined) => {
    if (!scope) return Promise.resolve<{ groups: string[] | null; scoped: boolean }>({ groups: null, scoped: false });
    const q = new URLSearchParams({ kind: scope.kind, id: scope.id ?? "" });
    return req<{ groups: string[] | null; scoped: boolean }>(`/knowledge/scope?${q}`);
  },
  getTaskMeta: (repo: string, branch: string) =>
    req<TaskMeta>(`/task/meta?repo=${encodeURIComponent(repo)}&branch=${encodeURIComponent(branch)}`),
  /** Upsert a Delivery task's goal + milestones (goal ⇒ ≥1 milestone). Omitted
   *  fields are left untouched, so this does not clear the assigned template. */
  setTaskMeta: (repo: string, branch: string, goal: string, milestones: Milestone[]) =>
    req<{ repo: string; branch: string }>("/task/meta", {
      method: "POST",
      body: JSON.stringify({ repo, branch, goal, milestones }),
    }),
  /** Set (or clear) the task's knowledge scope; leaves everything else alone.
   *  Clearing is stated explicitly because "field omitted" already means
   *  "leave it alone". */
  setTaskScope: (repo: string, branch: string, scope: KnowledgeScope | null) =>
    req<{ repo: string; branch: string }>("/task/meta", {
      method: "POST",
      body: JSON.stringify(scope ? { repo, branch, scope } : { repo, branch, clearScope: true }),
    }),
  /** Assign the agent template a task runs with; leaves goal/milestones alone. */
  setTaskTemplate: (repo: string, branch: string, template: string) =>
    req<{ repo: string; branch: string }>("/task/meta", {
      method: "POST",
      body: JSON.stringify({ repo, branch, template }),
    }),
};
