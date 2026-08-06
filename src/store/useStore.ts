import { create } from "zustand";
import { hostagent, type LiveTask } from "@/lib/hostagent";
import {
  type SoloAgent,
  type StaticTemplate,
  defaultSolos,
  defaultStaticTpls,
  defaultProviders,
  defaultDynamicPrompt,
  defaultGlobalPrompt,
} from "@/lib/templates";
import { providersApi, type ProviderInput, type ProviderView } from "@/lib/providers";
import type { ToolDef } from "@/lib/tools";
import i18n, { currentLang, type Lang } from "@/i18n";

export type Theme = "dark" | "light";
export type DeliveryStatus = "inbox" | "working" | "done";
export type CI = "none" | "passed" | "running" | "failed";
export type ReviewTab = "task" | "diff" | "source" | "artifacts" | "evidence";
export type Source = "mock" | "live";

export interface Pipeline {
  name: string;
  steps: string;
  /** dot color */
  color: string;
}

export interface DeliveryTask {
  id: string; // JIRA label e.g. WEB-241
  title: string;
  project: string; // acme/web-app
  branch: string;
  target: string;
  worktree: string;
  agent: string;
  /** gradient avatar or muted */
  agentGradient: boolean;
  add: string; // +142
  del: string; // −38
  files: number;
  ci: CI;
  status: DeliveryStatus;
  active: boolean;
  review: boolean;
  pipeline: Pipeline;
  /** evidence flavor for the drawer */
  evidence: "vrt" | "api";
  merged?: string;
  /** true when backed by a real host-agent worktree (not mock). */
  live?: boolean;
  /** absolute worktree path (live tasks) — used to launch a sandbox agent. */
  worktreePath?: string;
}

export interface NotifItem {
  id: string;
  kind: "ci" | "artifact" | "status" | "agent";
  title: string;
  detail: string;
  /** legacy pre-rendered relative string (mock items). */
  time?: string;
  /** real event timestamp (epoch ms); rendered relative in the UI. */
  ts?: number;
  /** dot tone; overrides the kind-based color (e.g. failed CI = error). */
  tone?: "ok" | "error" | "info";
  read: boolean;
}

interface State {
  theme: Theme;
  toggleTheme: () => void;

  language: Lang;
  setLanguage: (l: Lang) => void;

  notifOpen: boolean;
  toggleNotif: () => void;
  notifications: NotifItem[];
  markAllRead: () => void;
  addNotif: (n: Omit<NotifItem, "id" | "read" | "ts"> & { id?: string }) => void;

  tasks: DeliveryTask[];
  moveTask: (id: string, status: DeliveryStatus) => void;

  reviewId: string | null;
  reviewTab: ReviewTab;
  openReview: (id: string) => void;
  closeReview: () => void;
  setReviewTab: (t: ReviewTab) => void;

  // Live host-agent integration
  source: Source;
  connecting: boolean;
  liveError: string | null;
  connectHostAgent: () => Promise<void>;
  autoConnectHostAgent: () => Promise<void>;
  disconnectHostAgent: () => void;
  refreshLive: () => Promise<void>;

  // Agent templates (persisted to localStorage)
  solos: SoloAgent[];
  staticTpls: StaticTemplate[];
  dynamicPrompt: string;
  globalPrompt: string;
  upsertSolo: (s: SoloAgent) => void;
  deleteSolo: (id: string) => void;
  upsertStaticTpl: (t: StaticTemplate) => void;
  deleteStaticTpl: (id: string) => void;
  setDynamicPrompt: (p: string) => void;
  setGlobalPrompt: (p: string) => void;
  tools: ToolDef[];
  upsertTool: (t: ToolDef) => void;
  deleteTool: (id: string) => void;

  // Providers (LLM connections). Non-secret config persists to localStorage;
  // secrets live in the OS keychain (host side) — hasSecret comes from the gateway.
  providers: ProviderInput[];
  providerViews: ProviderView[]; // live view from the gateway (has hasSecret); transient
  providerError: string | null;
  syncProviders: () => Promise<void>;
  upsertProvider: (p: ProviderInput) => Promise<void>;
  deleteProvider: (name: string) => Promise<void>;
  setProviderSecret: (name: string, value: string) => Promise<void>;
}

/* ── template persistence (localStorage) ── */

const TPL_KEY = "orchestra.templates.v1";

interface TemplatesBlob {
  solos: SoloAgent[];
  staticTpls: StaticTemplate[];
  providers: ProviderInput[];
  tools: ToolDef[];
  dynamicPrompt: string;
  globalPrompt: string;
}

// Ensure a solo has a provider binding (migrates pre-provider blobs).
function normSolo(s: SoloAgent): SoloAgent {
  return { ...s, providerId: s.providerId || "anthropic", model: s.model || "claude-opus-4-8" };
}

/** The shipped catalog in a given language (defaults to the active one). */
function templateDefaults(lng?: string): TemplatesBlob {
  return {
    solos: defaultSolos(lng),
    staticTpls: defaultStaticTpls(lng),
    providers: defaultProviders,
    tools: [],
    dynamicPrompt: defaultDynamicPrompt(lng),
    globalPrompt: defaultGlobalPrompt(lng),
  };
}

function loadTemplates(): TemplatesBlob {
  const defaults = templateDefaults();
  try {
    const raw = localStorage.getItem(TPL_KEY);
    if (!raw) return defaults;
    const parsed = JSON.parse(raw) as Partial<TemplatesBlob>;
    // Union in any default provider missing from the persisted set by name, so
    // newly-shipped built-ins (e.g. the github tool provider) appear for
    // existing users without wiping their customizations.
    const persistedProviders = parsed.providers ?? defaults.providers;
    const haveNames = new Set(persistedProviders.map((p) => p.name));
    const providers = [...persistedProviders, ...defaults.providers.filter((p) => !haveNames.has(p.name))];
    return {
      solos: (parsed.solos ?? defaults.solos).map(normSolo),
      staticTpls: parsed.staticTpls ?? defaults.staticTpls,
      providers,
      tools: parsed.tools ?? defaults.tools,
      dynamicPrompt: parsed.dynamicPrompt ?? defaults.dynamicPrompt,
      globalPrompt: parsed.globalPrompt ?? defaults.globalPrompt,
    };
  } catch {
    return defaults;
  }
}

/* ── language switching ──
 *
 * Role prompts and template descriptions are seeded from the active locale, so
 * switching language has to move them too — otherwise an English UI still
 * briefs its agents in Japanese. But those same fields are editable, and an
 * edit is not something a language switch may discard.
 *
 * So the rule is: a value that is still byte-identical to the default of the
 * language we are leaving gets re-seeded from the language we are entering;
 * anything else is the user's and is left alone. Solos are matched per id, so
 * an untouched Planner follows the UI while a hand-written one next to it does
 * not. */

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

function reseedTemplates(blob: TemplatesBlob, from: string, to: string): TemplatesBlob {
  const was = templateDefaults(from);
  const now = templateDefaults(to);

  const byId = <T extends { id: string }>(xs: T[]) => new Map(xs.map((x) => [x.id, x]));
  const wasSolos = byId(was.solos);
  const nowSolos = byId(now.solos);
  const wasTpls = byId(was.staticTpls);
  const nowTpls = byId(now.staticTpls);

  return {
    ...blob,
    solos: blob.solos.map((s) => (same(s, wasSolos.get(s.id)) ? nowSolos.get(s.id)! : s)),
    staticTpls: blob.staticTpls.map((t) => (same(t, wasTpls.get(t.id)) ? nowTpls.get(t.id)! : t)),
    dynamicPrompt: blob.dynamicPrompt === was.dynamicPrompt ? now.dynamicPrompt : blob.dynamicPrompt,
    globalPrompt: blob.globalPrompt === was.globalPrompt ? now.globalPrompt : blob.globalPrompt,
  };
}

function persistTemplates(s: TemplatesBlob) {
  try {
    localStorage.setItem(
      TPL_KEY,
      JSON.stringify({ solos: s.solos, staticTpls: s.staticTpls, providers: s.providers, tools: s.tools, dynamicPrompt: s.dynamicPrompt, globalPrompt: s.globalPrompt }),
    );
  } catch {
    /* ignore (e.g. storage disabled) */
  }
}

/** Map a host-agent worktree task onto the board's DeliveryTask shape. */
function mapLive(t: LiveTask): DeliveryTask {
  const hasChanges = t.files > 0;
  return {
    id: t.id,
    title: t.branch.replace(/^(feat|fix|chore)\//, ""),
    project: t.repo,
    branch: t.branch,
    target: t.target,
    worktree: t.worktreePath.split("/").pop() ?? t.worktreePath,
    agent: "worktree",
    agentGradient: true,
    add: `+${t.additions}`,
    del: `−${t.deletions}`,
    files: t.files,
    ci: t.ci,
    status: hasChanges ? "working" : "inbox",
    active: false,
    review: hasChanges,
    pipeline: PIPELINES[0],
    evidence: "api",
    live: true,
    worktreePath: t.worktreePath,
  };
}

export const PIPELINES: Pipeline[] = [
  { name: "Solo — Coder", steps: "単体 · 1 agent", color: "var(--avatar-mut)" },
  { name: "Supervisor — Backend", steps: "中央supervisor · 3 agents", color: "var(--ac)" },
  { name: "Graph — Frontend", steps: "固定工程 · plan→build→vrt", color: "var(--cyan)" },
  { name: "Graph — Full-stack", steps: "固定工程 · 5 steps", color: "#67c9a4" },
  { name: "Dynamic Orchestration", steps: "meta-orchestrator · 自動構成", color: "var(--purple)" },
  { name: "Solo — Docs", steps: "単体 · 1 agent", color: "#e0a83e" },
];

const g = (b: boolean) => b;

export const initialTasks: DeliveryTask[] = [
  {
    id: "WEB-241", title: "決済リトライ処理の追加", project: "acme/web-app",
    branch: "feat/retry", target: "release/2.4", worktree: "wt-3a1f", agent: "Builder", agentGradient: g(true),
    add: "+142", del: "−38", files: 3, ci: "passed", status: "inbox", active: true, review: false,
    pipeline: PIPELINES[1], evidence: "api",
  },
  {
    id: "API-88", title: "Rate limit middleware", project: "acme/api",
    branch: "feat/ratelimit", target: "main", worktree: "wt-9c02", agent: "Coder", agentGradient: g(false),
    add: "+88", del: "−12", files: 2, ci: "running", status: "inbox", active: false, review: false,
    pipeline: PIPELINES[0], evidence: "api",
  },
  {
    id: "WEB-233", title: "フォーム検証の刷新", project: "acme/web-app",
    branch: "feat/forms", target: "main", worktree: "wt-1b7d", agent: "Coder", agentGradient: g(false),
    add: "+64", del: "−9", files: 4, ci: "passed", status: "inbox", active: false, review: false,
    pipeline: PIPELINES[2], evidence: "vrt",
  },
  {
    id: "WEB-219", title: "検索インデックスの再構築", project: "acme/web-app",
    branch: "fix/index", target: "main", worktree: "wt-77ac", agent: "Builder", agentGradient: g(true),
    add: "+311", del: "−96", files: 7, ci: "passed", status: "working", active: true, review: true,
    pipeline: PIPELINES[1], evidence: "vrt",
  },
  {
    id: "API-72", title: "Webhook 署名検証", project: "acme/api",
    branch: "feat/webhook-sig", target: "main", worktree: "wt-4d51", agent: "Reviewer", agentGradient: g(false),
    add: "+53", del: "−4", files: 2, ci: "failed", status: "working", active: false, review: true,
    pipeline: PIPELINES[3], evidence: "api",
  },
  {
    id: "INF-12", title: "CIキャッシュの最適化", project: "acme/infra",
    branch: "merged", target: "main", worktree: "wt-2200", agent: "Coder", agentGradient: g(false),
    add: "+40", del: "−120", files: 5, ci: "passed", status: "done", active: false, review: false,
    pipeline: PIPELINES[0], evidence: "api", merged: "merged → main",
  },
  {
    id: "DOC-5", title: "APIリファレンス自動生成", project: "acme/docs",
    branch: "merged", target: "release/2.4", worktree: "wt-0f9a", agent: "Builder", agentGradient: g(true),
    add: "+890", del: "−12", files: 14, ci: "passed", status: "done", active: false, review: false,
    pipeline: PIPELINES[4], evidence: "vrt", merged: "merged → release/2.4",
  },
];

const initialNotifs: NotifItem[] = [
  { id: "n1", kind: "ci", title: "検索インデックスの再構築 — CI 5/5 合格", detail: "セルフレビュー解禁 · web-app", time: "2分前", read: false },
  { id: "n2", kind: "ci", title: "Webhook 署名検証 — CI 失敗", detail: "typecheck 2 errors · api", time: "8分前", read: false },
  { id: "n3", kind: "status", title: "決済リトライ処理の追加が起票されました", detail: "acme/web-app · Builder 稼働中", time: "14分前", read: false },
  { id: "n4", kind: "artifact", title: "Storybook VRT の成果物が届きました", detail: "3 stories · 0 diffs", time: "31分前", read: true },
];

export const useStore = create<State>((set) => ({
  theme: "dark",
  toggleTheme: () => set((s) => ({ theme: s.theme === "dark" ? "light" : "dark" })),

  language: currentLang(),
  setLanguage: (l) =>
    set((s) => {
      if (l === s.language) return {};
      void i18n.changeLanguage(l); // also writes the localStorage cache
      const next = reseedTemplates(
        { solos: s.solos, staticTpls: s.staticTpls, providers: s.providers, tools: s.tools, dynamicPrompt: s.dynamicPrompt, globalPrompt: s.globalPrompt },
        s.language,
        l,
      );
      persistTemplates(next);
      return { language: l, solos: next.solos, staticTpls: next.staticTpls, dynamicPrompt: next.dynamicPrompt, globalPrompt: next.globalPrompt };
    }),

  notifOpen: false,
  toggleNotif: () => set((s) => ({ notifOpen: !s.notifOpen })),
  notifications: initialNotifs,
  markAllRead: () => set((s) => ({ notifications: s.notifications.map((n) => ({ ...n, read: true })) })),
  // Prepend a live event; keep the list bounded so it can't grow unbounded.
  addNotif: (n) =>
    set((s) => ({
      notifications: [
        { ...n, id: n.id ?? `n-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`, read: false, ts: Date.now() },
        ...s.notifications,
      ].slice(0, 50),
    })),

  tasks: initialTasks,
  moveTask: (id, status) => set((s) => ({ tasks: s.tasks.map((t) => (t.id === id ? { ...t, status } : t)) })),

  reviewId: null,
  reviewTab: "task",
  openReview: (id) => set({ reviewId: id, reviewTab: "task" }),
  closeReview: () => set({ reviewId: null }),
  setReviewTab: (t) => set({ reviewTab: t }),

  source: "mock",
  connecting: false,
  liveError: null,
  connectHostAgent: async () => {
    set({ connecting: true, liveError: null });
    try {
      const ok = await hostagent.health();
      if (!ok) throw new Error(i18n.t("errors.hostAgentOffline"));
      const live = await hostagent.tasks();
      set({ tasks: live.map(mapLive), source: "live", connecting: false });
    } catch (e) {
      set({ connecting: false, liveError: e instanceof Error ? e.message : String(e) });
    }
  },
  autoConnectHostAgent: async () => {
    // One-shot best-effort connect at startup so Delivery/Daily aren't stuck on
    // mock data when the host agent is available. Retries briefly (the sidecar
    // may still be coming up), stays mock silently if it never responds (no
    // scary liveError), and never overrides a manual toggle already in flight.
    if (useStore.getState().source === "live" || useStore.getState().connecting) return;
    for (let i = 0; i < 5; i++) {
      try {
        if (await hostagent.health()) {
          await useStore.getState().connectHostAgent();
          return;
        }
      } catch {
        /* host agent not ready yet */
      }
      if (useStore.getState().source === "live") return; // a manual connect won the race
      await new Promise((r) => setTimeout(r, 800));
    }
  },
  disconnectHostAgent: () => set({ tasks: initialTasks, source: "mock", liveError: null }),
  refreshLive: async () => {
    const st = useStore.getState();
    if (st.source !== "live") return;
    try {
      const live = await hostagent.tasks();
      const prevCI: Record<string, string> = {};
      st.tasks.forEach((t) => { prevCI[t.id] = t.ci; });
      const next = live.map(mapLive);
      // Emit a notification on each CI transition into a terminal state.
      next.forEach((t) => {
        const before = prevCI[t.id];
        if (before && before !== t.ci && (t.ci === "passed" || t.ci === "failed")) {
          st.addNotif({
            kind: "ci",
            tone: t.ci === "passed" ? "ok" : "error",
            title: `${t.title} — CI ${i18n.t(t.ci === "passed" ? "common.passed" : "common.failed")}`,
            detail: `${t.project} · ${t.branch}`,
          });
        }
      });
      set({ tasks: next });
    } catch (e) {
      set({ liveError: e instanceof Error ? e.message : String(e) });
    }
  },

  // Agent templates
  ...loadTemplates(),
  upsertSolo: (s) =>
    set((st) => {
      const solos = st.solos.some((x) => x.id === s.id)
        ? st.solos.map((x) => (x.id === s.id ? s : x))
        : [...st.solos, s];
      persistTemplates({ ...st, solos });
      return { solos };
    }),
  deleteSolo: (id) =>
    set((st) => {
      const solos = st.solos.filter((x) => x.id !== id);
      persistTemplates({ ...st, solos });
      return { solos };
    }),
  upsertStaticTpl: (t) =>
    set((st) => {
      const staticTpls = st.staticTpls.some((x) => x.id === t.id)
        ? st.staticTpls.map((x) => (x.id === t.id ? t : x))
        : [...st.staticTpls, t];
      persistTemplates({ ...st, staticTpls });
      return { staticTpls };
    }),
  deleteStaticTpl: (id) =>
    set((st) => {
      const staticTpls = st.staticTpls.filter((x) => x.id !== id);
      persistTemplates({ ...st, staticTpls });
      return { staticTpls };
    }),
  setDynamicPrompt: (p) =>
    set((st) => {
      persistTemplates({ ...st, dynamicPrompt: p });
      return { dynamicPrompt: p };
    }),
  setGlobalPrompt: (p) =>
    set((st) => {
      persistTemplates({ ...st, globalPrompt: p });
      return { globalPrompt: p };
    }),
  upsertTool: (t) =>
    set((st) => {
      const tools = st.tools.some((x) => x.id === t.id)
        ? st.tools.map((x) => (x.id === t.id ? t : x))
        : [...st.tools, t];
      persistTemplates({ ...st, tools });
      return { tools };
    }),
  deleteTool: (id) =>
    set((st) => {
      const tools = st.tools.filter((x) => x.id !== id);
      persistTemplates({ ...st, tools });
      return { tools };
    }),

  // Providers
  providerViews: [],
  providerError: null,
  syncProviders: async () => {
    try {
      const views = await providersApi.sync(useStore.getState().providers);
      set({ providerViews: views, providerError: null });
    } catch (e) {
      set({ providerError: e instanceof Error ? e.message : String(e) });
    }
  },
  upsertProvider: async (p) => {
    set((st) => {
      const providers = st.providers.some((x) => x.name === p.name)
        ? st.providers.map((x) => (x.name === p.name ? p : x))
        : [...st.providers, p];
      persistTemplates({ ...st, providers });
      return { providers };
    });
    await useStore.getState().syncProviders();
  },
  deleteProvider: async (name) => {
    set((st) => {
      const providers = st.providers.filter((x) => x.name !== name);
      persistTemplates({ ...st, providers });
      return { providers };
    });
    try {
      const views = await providersApi.remove(name);
      set({ providerViews: views });
    } catch (e) {
      set({ providerError: e instanceof Error ? e.message : String(e) });
    }
  },
  setProviderSecret: async (name, value) => {
    try {
      const views = await providersApi.setSecret(name, value);
      set({ providerViews: views, providerError: null });
    } catch (e) {
      set({ providerError: e instanceof Error ? e.message : String(e) });
    }
  },
}));
