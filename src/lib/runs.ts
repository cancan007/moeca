// Client for the host agent's manual agent-run history: record a run launched
// from Delivery and list the recent ones, so manual runs share the run-history
// + optimization loop with scheduled runs.

const BASE = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";

export interface AgentRun {
  id: number;
  source: string;
  name: string;
  repo: string;
  branch: string;
  task: string;
  template: string;
  templateRef: string;
  containerId: string;
  runId: string;
  createdAt: string;
}

export interface RunRecordInput {
  name: string;
  repo: string;
  branch: string;
  task?: string;
  template?: string;
  templateRef?: string;
  containerId?: string;
  runId?: string;
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

export const runs = {
  /** Persist a manually-launched run. Best-effort; callers ignore failures. */
  record: (r: RunRecordInput) => req<{ id: number }>("/runs", { method: "POST", body: JSON.stringify(r) }),
  /** Recent manual runs, newest first. */
  list: (limit?: number) => req<{ runs: AgentRun[] }>(`/runs${limit ? `?limit=${limit}` : ""}`).then((r) => r.runs),
};
