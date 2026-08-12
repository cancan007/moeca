// Client for the host agent's Daily task-source API: pull-model ingest of
// tickets from external systems of record (Jira / Trello / Notion). The host
// agent fetches through the security gateway, so no credentials touch the
// frontend. Delivery's git-backed tasks are separate (see hostagent.ts).

const BASE = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";

export interface Ticket {
  id: string; // source-qualified, e.g. "jira:PROJ-42"
  source: string; // jira | trello | notion | demo
  title: string;
  body: string;
  url: string;
  state: string; // open | in_progress | closed
  repo: string;
  branch: string;
  labels: string[];
  updatedAt: string; // RFC3339
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

export type SourceType = "jira" | "trello" | "notion";

export interface ScheduleRun {
  id: number;
  scheduleId: string;
  name: string;
  perspective: string;
  scheduledAt: string; // RFC3339
  status: "executed" | "missed" | "failed";
  /** The directory this run produced its artifacts in. Daily runs are not git
   *  work — a schedule produces a report or a rendered video, not a branch to
   *  review — so an occurrence points at a plain directory. Empty when nothing
   *  was launched (a schedule with no template bound). */
  outputDir: string;
  containerId: string; // the sandbox whose logs are the session record (bare run)
  runId: string; // the orchestrator run id (template DAG run)
  template: string; // display label of the template used
  /** Only on occurrences recorded before Daily was separated from git. */
  repo?: string;
  branch?: string;
}

/** One file a scheduled run produced. */
export interface Artifact {
  path: string; // relative to the run's output directory
  name: string;
  size: number;
  kind: "video" | "image" | "audio" | "text" | "file";
  modTime: string;
}

export const daily = {
  /** Configured task sources (e.g. ["jira","notion"]). */
  sources: () => req<{ sources: string[] }>("/daily/sources").then((r) => r.sources),
  /** Explicitly pull from one source; returns the freshly-pulled tickets. */
  pull: (source: string) =>
    req<{ pulled: number; tickets: Ticket[] }>(
      `/daily/pull?source=${encodeURIComponent(source)}`,
      { method: "POST" },
    ),
  /** All stored pulled tickets, optionally filtered by source. */
  tickets: (source?: string) =>
    req<{ tickets: Ticket[] }>(
      `/daily/tickets${source ? `?source=${encodeURIComponent(source)}` : ""}`,
    ).then((r) => r.tickets),
  /** Recent schedule occurrences (executed / missed) for the run history. */
  runs: (limit?: number) =>
    req<{ runs: ScheduleRun[] }>(`/daily/runs${limit ? `?limit=${limit}` : ""}`).then((r) => r.runs),
  /** What a scheduled run produced, for the gallery. */
  artifacts: (runId: number) =>
    req<{ artifacts: Artifact[] }>(`/daily/artifacts?run=${runId}`).then((r) => r.artifacts),
  /**
   * URL of one artifact's bytes — used directly as a <video>/<img>/<audio> src
   * and as the download link.
   *
   * The occurrence id is the only handle: the host agent resolves it to the
   * directory that run wrote into, so no path outside it is addressable. Only
   * media is served inline; anything else comes back as a download whatever
   * this flag says.
   */
  artifactUrl: (runId: number, path: string, download = false) =>
    `${BASE}/daily/artifact?run=${runId}&path=${encodeURIComponent(path)}${download ? "&download=1" : ""}`,
  /** Remove one artifact. A gallery that only grows is one nothing can be
   *  found in, so deleting is part of reviewing rather than an admin task. */
  deleteArtifact: (runId: number, path: string) =>
    req<{ deleted: string }>(`/daily/artifact?run=${runId}&path=${encodeURIComponent(path)}`, { method: "DELETE" }),
  /** Remove an occurrence: its output directory and its history row. */
  deleteRun: (runId: number) =>
    req<{ deleted: number }>(`/daily/run?run=${runId}`, { method: "DELETE" }),
  /** Promote a pulled ticket into a Delivery worktree (branch ticket/<id>). */
  promote: (id: string, repo?: string) =>
    req<{ ticket: string; repo: string; branch: string; worktreePath: string }>(
      "/daily/promote",
      { method: "POST", body: JSON.stringify({ id, repo: repo ?? "" }) },
    ),
  /** Add a runtime-configured task source (persisted). */
  addSource: (type: SourceType, name?: string) =>
    req<{ name: string; type: string }>("/daily/sources/config", {
      method: "POST",
      body: JSON.stringify({ type, name: name ?? "" }),
    }),
  /** Remove a runtime-configured task source. */
  removeSource: (name: string) =>
    req<{ removed: string }>(`/daily/sources/config?name=${encodeURIComponent(name)}`, {
      method: "DELETE",
    }),
};
