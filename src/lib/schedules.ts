// Client for the Orchestra host agent's schedules API (cron-driven tasks the
// host agent runs on the user's behalf). The service runs on loopback as a
// Tauri sidecar; in dev it is started with `cd hostagent && go run . -config
// config.json`.

import i18n from "@/i18n";

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
  /** Legacy free-text label. Nothing reads it and nothing writes it any more —
   *  a schedule's composition is its templateRef. Kept only because the host
   *  agent still stores and returns the column. */
  task?: string;
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
  active?: boolean;
  goal?: string;
  milestones?: Milestone[];
  templateLabel?: string;
  templateRef?: string;
  runSpec?: unknown;
}

/** The instruction a schedule's agents actually receive.
 *
 *  The goal used to be the whole of it, which meant the milestones — typed into
 *  the same form, right below it — reached nobody. A schedule named "犬の画像作成"
 *  with the milestones "可愛い芝犬をモデルにする" and "画像として出力" was handed
 *  the four characters of its goal, "画像作成", and its agents had no way to know
 *  a dog was wanted. Anything the form asks for has to reach the run, or the
 *  form is lying about what it configures.
 *
 *  Milestones are numbered rather than run together: they are acceptance
 *  criteria, and a list reads as one. */
export function scheduleTask(s: { name?: string; goal?: string; milestones?: { title: string }[] }): string {
  const goal = (s.goal ?? "").trim();
  const name = (s.name ?? "").trim();
  const head = goal || name;
  const titles = (s.milestones ?? []).map((m) => m.title.trim()).filter(Boolean);
  if (titles.length === 0) return head;
  // The name is kept alongside a goal only when it adds something: "犬の画像作成"
  // next to "画像作成" is the difference between knowing the subject and not.
  const lines = goal && name && name !== goal ? [`${goal}（${name}）`] : [head];
  lines.push("", i18n.t("daily.acceptanceCriteria"));
  titles.forEach((title, i) => lines.push(`${i + 1}. ${title}`));
  return lines.join("\n");
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
  /** Fire a schedule now, without waiting for its cron. Works on a paused one:
   *  pausing stops the clock, not the operator. */
  runNow: (id: string) =>
    req<{ runId: string; outputDir: string }>("/schedules/run", { method: "POST", body: JSON.stringify({ id }) }),
};
