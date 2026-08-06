// Client for the Orchestra host agent's schedules API (cron-driven tasks the
// host agent runs on the user's behalf). The service runs on loopback as a
// Tauri sidecar; in dev it is started with `cd hostagent && go run . -config
// config.json`.

const BASE = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";

export type SchedulePerspective = "discovery" | "context-opt" | "automation";

export interface Milestone {
  title: string;
  done: boolean;
}

export interface Schedule {
  id: string;
  name: string;
  cron: string; // 5-field "m h dom mon dow"
  perspective: SchedulePerspective;
  task: string;
  active: boolean;
  lastRun: string; // RFC3339, empty if never
  runCount: number;
  goal: string;
  milestones: Milestone[];
  templateLabel: string;
  templateRef: string; // "solo:<id>" | "static:<id>" (for the prompt-edit loop)
  runSpec?: unknown; // compiled stages DAG the host agent forwards to /run
}

export interface ScheduleSpec {
  name: string;
  cron: string;
  perspective: SchedulePerspective;
  task: string;
  active?: boolean;
  goal?: string;
  milestones?: Milestone[];
  templateLabel?: string;
  templateRef?: string;
  runSpec?: unknown;
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

export const schedules = {
  async health(signal?: AbortSignal): Promise<boolean> {
    try {
      const res = await fetch(BASE + "/health", { signal });
      return res.ok;
    } catch {
      return false;
    }
  },
  list: () => req<{ schedules: Schedule[] }>("/schedules").then((r) => r.schedules),
  create: (spec: ScheduleSpec) =>
    req<Schedule>("/schedules", { method: "POST", body: JSON.stringify(spec) }),
  update: (id: string, spec: ScheduleSpec) =>
    req<Schedule>("/schedules/update", { method: "POST", body: JSON.stringify({ id, ...spec }) }),
  remove: (id: string) =>
    req<{ removed: string }>(`/schedules?id=${encodeURIComponent(id)}`, { method: "DELETE" }),
  toggle: (id: string) =>
    req<Schedule>("/schedules/toggle", { method: "POST", body: JSON.stringify({ id }) }),
};
