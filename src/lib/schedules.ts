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
  /** Knowledge scope: the node of the Knowledge graph this schedule may read.
   *  Absent means no scope, and such a run retrieves nothing at all; reaching
   *  the knowledge shared with everyone is the "global" scope, chosen. */
  scope?: KnowledgeScope;
  /** Files staged into every run's working directory before its agents start.
   *  Read-only here: they are added and removed through their own routes, not
   *  by writing the schedule, because the bytes travel separately. */
  attachments?: Attachment[];
}

/** One file a schedule hands its own agents.
 *
 *  Distinct from knowledge, which is a searchable corpus shared across
 *  schedules and reached only through retrieval. An attachment is an input that
 *  belongs to this task and is simply on disk when it starts — a reference
 *  picture, a CSV to summarise, a template to fill in. */
export interface Attachment {
  name: string;
  size: number;
  addedAt: string;
}

/** What a schedule may retrieve, named as a place in the Knowledge graph.
 *
 *  "global" carries no id and means the knowledge everyone shares. It is not
 *  "everything": it resolves to no groups at all, and globally-scoped sources
 *  reach the run anyway because the retrieval filter exempts them. Naming a
 *  node rather than a group list also means the scope follows the graph — a
 *  group added to the project later is included without re-saving. */
export interface KnowledgeScope {
  kind: "global" | "organization" | "project";
  id?: string;
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
  /** Send null to clear the scope, omit to leave it alone. */
  scope?: KnowledgeScope | null;
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

  /** Attach a file to a schedule. The bytes are copied host-side now rather
   *  than the path being remembered: a schedule fires unattended for months, and
   *  pointing at a file on this machine would mean a run that breaks when a
   *  folder is tidied — or worse, one that quietly picks up a later version.
   *
   *  Sent as multipart rather than through `req`, which sets a JSON content
   *  type; the browser writes its own boundary and must not be overridden. */
  async attach(id: string, file: File): Promise<Schedule> {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch(`${BASE}/daily/attachment?schedule=${encodeURIComponent(id)}`, {
      method: "POST",
      body: form,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
    }
    return res.json() as Promise<Schedule>;
  },

  detach: (id: string, name: string) =>
    req<Schedule>(
      `/daily/attachment?schedule=${encodeURIComponent(id)}&name=${encodeURIComponent(name)}`,
      { method: "DELETE" },
    ),
  /** Fire a schedule now, without waiting for its cron. Works on a paused one:
   *  pausing stops the clock, not the operator. */
  runNow: (id: string) =>
    req<{ runId: string; outputDir: string }>("/schedules/run", { method: "POST", body: JSON.stringify({ id }) }),
};
