import { useState, useEffect, useCallback, type CSSProperties } from "react";
import { schedules as schedulesApi, scheduleTask, type Schedule as LiveSchedule, type SchedulePerspective } from "@/lib/schedules";
import { daily as dailyApi, type Ticket, type ScheduleRun } from "@/lib/daily";
import { useStore } from "@/store/useStore";
import { templateOptions, compileRef, buildRunSpec, type TemplateStores } from "@/lib/agentTemplates";
import { ArtifactGallery, DailyRunDrawer } from "./ArtifactGallery";
import { calendarRuns, runStateLabel, type CalendarRun } from "./calendarRuns";
import { parseCron } from "@/lib/cron";
import { RunOptimizer } from "@/features/runs/RunOptimizer";
import i18n from "@/i18n";
import { useTranslation } from "react-i18next";

// ---------------------------------------------------------------------------
// shared data
// ---------------------------------------------------------------------------

// A perspective is the backend's own id everywhere in this file — it is what a
// schedule and an occurrence actually store, and it survives a language switch.
// Only perspectiveLabel turns one into words.
type Perspective = SchedulePerspective;

const PERSPECTIVES: Perspective[] = ["discovery", "context-opt", "automation"];

function kindColor(k: string): string {
  if (k === "discovery") return "#d39a4e";
  if (k === "context-opt") return "#5b9fe8";
  return "#5fbf95";
}

function perspectiveLabel(p: string): string {
  if (p === "discovery") return i18n.t("daily.perspective.discovery");
  if (p === "context-opt") return i18n.t("daily.perspective.contextOpt");
  return i18n.t("daily.perspective.automation");
}
function startOfDayMs(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

function perspectiveColor(p: string): string {
  return kindColor(p);
}

// color for a normalized external-ticket state (open | in_progress | closed)
function ticketStateColor(state: string): string {
  if (state === "closed") return "var(--tx-faint)";
  if (state === "in_progress") return "#5b9fe8";
  return "#5fbf95";
}

// label + color for a schedule occurrence status.
//
// "executed" now means a run that was launched and has not been seen to finish
// — the outcome arrives later, as done / empty / failed. `empty` is the one
// worth having: nothing failed, so it is not a failure, but the run produced no
// files, which is not a success either and used to be indistinguishable from
// one.
function runStatusStyle(status: string): { label: string; color: string } {
  if (status === "missed") return { label: i18n.t("daily.runState.missed"), color: "#d39a4e" };
  if (status === "failed") return { label: i18n.t("daily.runState.failed"), color: "#e0654e" };
  if (status === "empty") return { label: i18n.t("daily.runState.empty"), color: "#d39a4e" };
  if (status === "done") return { label: i18n.t("daily.runState.done"), color: "#5fbf95" };
  return { label: i18n.t("daily.runState.executed"), color: "#5b9fe8" };
}

// compact "MM/DD HH:mm" from an RFC3339 timestamp (local time)
function shortWhen(rfc3339: string): string {
  const d = new Date(rfc3339);
  if (isNaN(d.getTime())) return rfc3339;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

interface Schedule {
  name: string;
  kind: Perspective;
  cron: string;
  pipeName: string;
  review?: string;
}

const schedules: Schedule[] = [
  { name: "競合UI監視", kind: "discovery", cron: "0 8 * * *", pipeName: "Scout Pipeline" },
  { name: "コンテキスト最適化", kind: "context-opt", cron: "0 18 * * *", pipeName: "Tuner Pipeline", review: "レビュープロセスあり" },
  { name: "日次レポート配信", kind: "automation", cron: "30 9 * * *", pipeName: "Runner Pipeline" },
  { name: "週次サマリ", kind: "discovery", cron: "0 7 * * 1", pipeName: "Scout Pipeline" },
];

// runs generated per calendar date --------------------------------------------


// The offline demo calendar. The live one is derived from real schedules and
// recorded occurrences (see calendarRuns).
function mockRunsForDate(d: Date): CalendarRun[] {
  const at = (time: string, name: string, perspective: string): CalendarRun =>
    ({ time, name, perspective, scheduleId: name, active: true });
  const runs = [
    at("08:00", "競合UI監視", "discovery"),
    at("09:30", "日次レポート配信", "automation"),
    at("18:00", "コンテキスト最適化", "context-opt"),
  ];
  if (d.getDay() === 1) runs.unshift(at("07:00", "週次サマリ", "discovery"));
  return runs.slice().sort((a, b) => a.time.localeCompare(b.time));
}

// date helpers ----------------------------------------------------------------

// Weekday and month names come from Intl rather than a hand-written table, so
// they follow the chosen language (and its conventions — "Mon" vs "月") without
// a translation entry per name. 2024-01-07 was a Sunday, which anchors index 0.
function weekdayNames(): string[] {
  const fmt = new Intl.DateTimeFormat(i18n.language, { weekday: "short" });
  return Array.from({ length: 7 }, (_, i) => fmt.format(new Date(2024, 0, 7 + i)));
}

function monthName(m: number): string {
  return new Intl.DateTimeFormat(i18n.language, { month: "long" }).format(new Date(2024, m, 1));
}

/** "January 2026" / "2026年1月" / "2026年1月" */
function monthYearLabel(d: Date): string {
  return new Intl.DateTimeFormat(i18n.language, { year: "numeric", month: "long" }).format(d);
}

/** "Jan 5" / "1月5日" — day granularity, no year. */
function monthDayLabel(d: Date): string {
  return new Intl.DateTimeFormat(i18n.language, { month: "short", day: "numeric" }).format(d);
}

/** "Mon, Jan 5" / "1月5日(月)" */
function weekdayDayLabel(d: Date): string {
  return new Intl.DateTimeFormat(i18n.language, { month: "short", day: "numeric", weekday: "short" }).format(d);
}

/** Midnight today, in the user's own timezone. */
function startOfToday(): Date {
  const n = new Date();
  return new Date(n.getFullYear(), n.getMonth(), n.getDate());
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}
function addDays(d: Date, n: number): Date {
  const r = new Date(d);
  r.setDate(d.getDate() + n);
  return r;
}
function pad2(n: number): string {
  return n < 10 ? `0${n}` : `${n}`;
}

// ---------------------------------------------------------------------------
// artifact model (gallery + preview modal)
// ---------------------------------------------------------------------------

type ArtType = "video" | "image" | "text" | "voice";

interface Artifact {
  type: ArtType;
  title: string;
  meta: string;
  grad?: string;
  duration?: string;
  imgCount?: number;
}

const VIDEO_GRAD = "linear-gradient(135deg,#1c1530,#2a1d44)";
const VOICE_GRAD = "linear-gradient(135deg,#241c0e,#332611)";
const IMAGE_GRAD = "linear-gradient(135deg,#0e2630,#123845)";

const RAW_MD = `# 論文サマリ: RAG最新

## 概要
- Retrieval-Augmented Generation の最新手法を 3 本の論文から要約。
- ハイブリッド検索 + 再ランクで **ヒット率 +18%**。

## 手法
1. Dense + Sparse のハイブリッド検索
2. Cross-encoder による再ランク
3. クエリ書き換え (HyDE)

> 圧縮により入力トークンを 24% 削減。`;

// ---------------------------------------------------------------------------
// small style helpers
// ---------------------------------------------------------------------------

const legendDot = (bg: string): CSSProperties => ({ width: 9, height: 9, borderRadius: 2, background: bg });
const legendItem: CSSProperties = { display: "flex", alignItems: "center", gap: 6, font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)" };

function segBtn(active: boolean): CSSProperties {
  return {
    font: "500 11px 'IBM Plex Sans'",
    color: active ? "var(--tx)" : "var(--tx-dim)",
    padding: "5px 11px",
    borderRadius: 7,
    background: active ? "var(--bg-card2)" : "transparent",
    border: active ? "1px solid var(--bd2)" : "1px solid transparent",
    cursor: "pointer",
  };
}

function calModeBtn(active: boolean): CSSProperties {
  return {
    font: "600 11px 'IBM Plex Sans'",
    color: active ? "#06121e" : "var(--tx3)",
    background: active ? "var(--ac)" : "transparent",
    padding: "5px 11px",
    borderRadius: 6,
    cursor: "pointer",
  };
}

function pill(active: boolean): CSSProperties {
  return {
    font: "600 11px 'IBM Plex Sans'",
    color: active ? "var(--ac)" : "var(--tx3)",
    padding: "8px 12px",
    borderRadius: 8,
    textAlign: "center",
    background: active ? "var(--tint-active)" : "var(--bg-card2)",
    border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd2)"}`,
    cursor: "pointer",
  };
}

function smallToggle(active: boolean): CSSProperties {
  return {
    font: "600 10px 'IBM Plex Mono'",
    color: active ? "var(--ac)" : "var(--tx-dim)",
    padding: "5px 9px",
    borderRadius: 6,
    background: active ? "var(--tint-active)" : "var(--bg-card2)",
    border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd2)"}`,
    cursor: "pointer",
  };
}

const cardBase: CSSProperties = {
  cursor: "pointer",
  background: "var(--bg-card2)",
  border: "1px solid var(--bd2)",
  borderRadius: 10,
  overflow: "hidden",
  display: "flex",
  flexDirection: "column",
};
const cardMeta: CSSProperties = { padding: "10px 11px", display: "flex", flexDirection: "column", gap: 5 };
const cardTitle: CSSProperties = { font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.35 };
const cardSub: CSSProperties = { font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" };
const cornerDot = (bg: string): CSSProperties => ({ position: "absolute", left: 7, top: 7, width: 8, height: 8, borderRadius: 2, background: bg });

function skel(w: string, mt?: number): CSSProperties {
  return { height: 5, width: w, background: "var(--skel)", borderRadius: 3, ...(mt ? { marginTop: mt } : {}) };
}

// waveform bars for a card (compact) and modal (wide)
const CARD_WAVE = [14, 28, 20, 34, 16, 24];
const CARD_WAVE_2 = [18, 30, 12, 26, 20];
const MODAL_WAVE = Array.from({ length: 64 }, (_, i) => 8 + Math.round(Math.abs(Math.sin(i * 0.7) + Math.cos(i * 0.31)) * 60));

// A schedule's composition is its bound agent template and nothing else: a
// single agent is a one-stage template, so multi-agent is already a choice in
// that same list. The modal used to carry a second "multi-agent composition"
// picker of hardcoded pipeline names — it compiled to nothing and only ever
// restored its own radio button, so a Solo could be shown next to a 3-step
// pipeline that never ran.

// ===========================================================================

export function Daily() {
  const { t } = useTranslation();
  const weekdays = weekdayNames();
  // top-level view
  const [dailyView, setDailyView] = useState<"gallery" | "calendar">("gallery");
  const dailyIsGallery = dailyView === "gallery";
  const dailyIsCalendar = dailyView === "calendar";

  // calendar
  const [calMode, setCalMode] = useState<"month" | "week" | "day">("month");
  // The current date is state, not a constant. It was hardcoded to a fixed day,
  // which put the "today" ring and the 今日 button on the wrong date — and,
  // once the calendar started splitting past from future, made a month of real
  // days look like the future and draw projected runs that never happened.
  //
  // It ticks because a dashboard is left open: crossing midnight with a frozen
  // "today" reintroduces exactly the same wrongness, just later.
  const [today, setToday] = useState<Date>(startOfToday);
  useEffect(() => {
    const t = setInterval(() => {
      setToday((prev) => {
        const now = startOfToday();
        return now.getTime() === prev.getTime() ? prev : now;
      });
    }, 60_000);
    return () => clearInterval(t);
  }, []);

  const [cursor, setCursor] = useState<Date>(startOfToday);
  const [selectedDate, setSelectedDate] = useState<Date>(startOfToday);

  // live backend (host agent schedules)
  const [live, setLive] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [liveError, setLiveError] = useState<string | null>(null);
  const [liveSchedules, setLiveSchedules] = useState<LiveSchedule[]>([]);

  // external task sources (Jira / Trello / Notion) — pull-model ingest
  const [sources, setSources] = useState<string[]>([]);
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [pulling, setPulling] = useState<string | null>(null);


  // agent templates (localStorage store) — for binding + prompt optimization
  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const providers = useStore((s) => s.providers);
  const tools = useStore((s) => s.tools);
  const tplStores: TemplateStores = { solos, staticTpls, providers, tools };
  const templateChoices = templateOptions(tplStores);
  // the run whose template prompt is being optimized
  const [optimizeRun, setOptimizeRun] = useState<ScheduleRun | null>(null);

  // schedule occurrence history (executed while up / missed while down)
  const [runs, setRuns] = useState<ScheduleRun[]>([]);
  const [artifactRun, setArtifactRun] = useState<ScheduleRun | null>(null);
  const refreshRuns = useCallback(async () => {
    try {
      setRuns(await dailyApi.runs(60));
    } catch {
      /* best-effort */
    }
  }, []);

  const refreshSchedules = useCallback(async () => {
    try {
      const list = await schedulesApi.list();
      setLiveSchedules(list);
      setLiveError(null);
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const refreshTickets = useCallback(async () => {
    try {
      setTickets(await dailyApi.tickets());
    } catch {
      /* tickets are best-effort; schedule errors surface via liveError */
    }
  }, []);

  async function connectLive() {
    setConnecting(true);
    setLiveError(null);
    try {
      const list = await schedulesApi.list();
      setLiveSchedules(list);
      setLive(true);
      setSources(await dailyApi.sources().catch(() => []));
      await refreshTickets();
      await refreshRuns();
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    } finally {
      setConnecting(false);
    }
  }
  function disconnectLive() {
    setLive(false);
    setLiveError(null);
    setLiveSchedules([]);
    setSources([]);
    setTickets([]);
    setRuns([]);
  }

  async function pullSource(source: string) {
    setPulling(source);
    try {
      await dailyApi.pull(source);
      await refreshTickets();
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    } finally {
      setPulling(null);
    }
  }

  const [promoting, setPromoting] = useState<string | null>(null);
  async function promoteTicket(ticket: Ticket) {
    setPromoting(ticket.id);
    try {
      const repo = ticket.repo || window.prompt(t("daily.promoteRepoPrompt")) || "";
      if (!repo) return;
      const res = await dailyApi.promote(ticket.id, repo);
      setLiveError(null);
      window.alert(t("daily.promoted", { repo: res.repo, branch: res.branch }));
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    } finally {
      setPromoting(null);
    }
  }

  // poll while live so recorded runs / external changes show up
  useEffect(() => {
    if (!live) return;
    const t = setInterval(() => {
      refreshSchedules();
      refreshRuns();
    }, 5000);
    return () => clearInterval(t);
  }, [live, refreshSchedules, refreshRuns]);

  async function toggleLiveSchedule(id: string) {
    try {
      await schedulesApi.toggle(id);
      await refreshSchedules();
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    }
  }
  // Which schedule is being launched by hand, so the row can say so. Firing a
  // schedule is not a scheduling question: waiting for a cron to come round was
  // the only way to try one, and the per-minute tick is aligned to when the
  // process started, so a schedule saved seconds after its tick simply did not
  // run and nothing said why.
  const [runningNow, setRunningNow] = useState<string | null>(null);
  async function runScheduleNow(id: string) {
    if (runningNow) return;
    setRunningNow(id);
    setLiveError(null);
    try {
      await schedulesApi.runNow(id);
      await Promise.all([refreshSchedules(), refreshRuns()]);
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    } finally {
      setRunningNow(null);
    }
  }

  async function removeLiveSchedule(id: string) {
    try {
      await schedulesApi.remove(id);
      await refreshSchedules();
    } catch (e) {
      setLiveError(e instanceof Error ? e.message : String(e));
    }
  }

  // modals
  const [newSchedOpen, setNewSchedOpen] = useState(false);
  // Which schedule the modal is editing; null means it is creating one. The
  // form is the same either way — an existing schedule should be as editable as
  // a new one is configurable.
  const [editingId, setEditingId] = useState<string | null>(null);
  // Set when the schedule's cron is one the form cannot express (a step, a
  // range, several distinct minutes). Saving then keeps the original rather
  // than rewriting it into whatever the form happens to show.
  const [cronOverride, setCronOverride] = useState<string | null>(null);
  const [artifact, setArtifact] = useState<Artifact | null>(null);

  // artifact modal sub-state
  const [playing, setPlaying] = useState(false);
  const [imgMode, setImgMode] = useState<"grid" | "h" | "v">("grid");
  const [textMode, setTextMode] = useState<"rendered" | "raw">("rendered");

  // draft (new schedule) form state
  const [draftName, setDraftName] = useState("");
  const [draftKind, setDraftKind] = useState<Perspective>("discovery");
  const [draftRepeat, setDraftRepeat] = useState(true);
  const [draftFreq, setDraftFreq] = useState<"daily" | "weekly" | "monthly">("daily");
  const [draftMonths, setDraftMonths] = useState<number[]>([]);
  const [draftWeeks, setDraftWeeks] = useState<number[]>([]);
  const [draftDows, setDraftDows] = useState<number[]>([]);
  const [draftTimes, setDraftTimes] = useState<string[]>(["08:00"]);
  const [draftGoal, setDraftGoal] = useState("");
  const [draftMilestones, setDraftMilestones] = useState<string[]>([""]);
  // "" only until the template list resolves; boundTemplateRef falls back to the
  // first template so a schedule is always bound to one.
  const [draftTemplateRef, setDraftTemplateRef] = useState("");

  const draftShowFreq = draftRepeat;
  const draftShowMonth = draftRepeat && draftFreq === "monthly";
  const draftShowWeek = draftRepeat && (draftFreq === "weekly" || draftFreq === "monthly");

  function toggleIn(arr: number[], v: number): number[] {
    return arr.includes(v) ? arr.filter((x) => x !== v) : [...arr, v];
  }

  function openArtifact(a: Artifact) {
    setArtifact(a);
    setPlaying(false);
    setImgMode("grid");
    setTextMode("rendered");
  }
  function closeArtifact() {
    setArtifact(null);
  }

  const stop = (e: React.MouseEvent) => e.stopPropagation();

  // calendar derived values
  const year = cursor.getFullYear();
  const month = cursor.getMonth();
  const monthStart = new Date(year, month, 1);
  const gridStart = addDays(monthStart, -monthStart.getDay());
  const calCells = Array.from({ length: 42 }, (_, i) => addDays(gridStart, i));

  const weekStart = addDays(cursor, -cursor.getDay());
  const weekDays = Array.from({ length: 7 }, (_, i) => addDays(weekStart, i));

  // Every calendar view goes through this, so the month grid, the week and day
  // views and the side panel can never disagree about a date.
  const runsForDate = useCallback(
    (d: Date): CalendarRun[] => (live ? calendarRuns(d, liveSchedules, runs, today) : mockRunsForDate(d)),
    [live, liveSchedules, runs, today],
  );
  const selRuns = runsForDate(selectedDate);
  const calLabel = monthYearLabel(new Date(year, month, 1));
  const weekEnd = addDays(weekStart, 6);
  const calWeekLabel = `${monthDayLabel(weekStart)} – ${monthDayLabel(weekEnd)}`;
  const calDayLabel = weekdayDayLabel(cursor);
  const calSelLabel = weekdayDayLabel(selectedDate);

  function stepCalendar(dir: number) {
    if (calMode === "month") setCursor(new Date(year, month + dir, 1));
    else if (calMode === "week") setCursor(addDays(cursor, dir * 7));
    else setCursor(addDays(cursor, dir));
  }

  const recurSummary = (() => {
    const times = draftTimes.join(" / ");
    if (!draftRepeat) return t("daily.recur.once", { times });
    if (draftFreq === "daily") return t("daily.recur.daily", { times });
    const sep = t("daily.recur.sep");
    const dows = draftDows.length ? draftDows.map((d) => weekdays[d]).join(sep) : t("daily.recur.anyWeekday");
    if (draftFreq === "weekly") return t("daily.recur.weekly", { dows, times });
    const months = draftMonths.length ? draftMonths.map((m) => monthName(m - 1)).join(sep) : t("daily.recur.anyMonth");
    const weeks = draftWeeks.length ? draftWeeks.map((w) => t("daily.recur.nthWeek", { n: w + 1 })).join(sep) : "";
    return `${months} ${weeks} ${dows} ${times}`.replace(/\s+/g, " ").trim();
  })();

  // Build a 5-field cron ("m h dom mon dow") from the draft form. The first
  // execution time drives minute/hour; weekly/monthly frequencies map to dow /
  // month lists. dom is left as "*".
  // Cron has one minute field and one hour field, so several times can only be
  // expressed when they share a minute ("0 8,18" = 08:00 and 18:00). Mixed
  // minutes would need a cross product — "0,30 8,18" also fires at 08:30 and
  // 18:00 — so those are refused rather than silently over-firing. Before this,
  // every time after the first was dropped without a word, which is what made a
  // schedule look wrong on the calendar.
  function buildCron(): string {
    if (cronOverride) return cronOverride;
    const times = draftTimes.length ? draftTimes : ["08:00"];
    const parts = times.map((t) => {
      const [hh, mm] = t.split(":");
      return { h: parseInt(hh, 10) || 0, m: parseInt(mm, 10) || 0 };
    });
    const sameMinute = parts.every((p) => p.m === parts[0].m);
    const m = String(parts[0].m);
    const h = sameMinute
      ? [...new Set(parts.map((p) => p.h))].sort((a, b) => a - b).join(",")
      : String(parts[0].h);
    let mon = "*";
    let dow = "*";
    if (draftRepeat && (draftFreq === "weekly" || draftFreq === "monthly") && draftDows.length) {
      dow = [...draftDows].sort((a, b) => a - b).join(",");
    }
    if (draftRepeat && draftFreq === "monthly" && draftMonths.length) {
      mon = [...draftMonths].sort((a, b) => a - b).join(",");
    }
    return `${m} ${h} * ${mon} ${dow}`;
  }

  /** The times buildCron cannot express, so the form can say so up front. */
  const droppedTimes =
    draftTimes.length > 1 && !draftTimes.every((t) => t.split(":")[1] === draftTimes[0].split(":")[1])
      ? draftTimes.slice(1)
      : [];

  function resetDraft() {
    setDraftName("");
    setDraftKind("discovery");
    setDraftRepeat(true);
    setDraftFreq("daily");
    setDraftMonths([]);
    setDraftWeeks([]);
    setDraftDows([]);
    setDraftTimes(["08:00"]);
    setDraftGoal("");
    setDraftMilestones([""]);
    setDraftTemplateRef("");
    setEditingId(null);
    setCronOverride(null);
  }

  /** Loads an existing schedule into the draft and opens the modal. */
  function editSchedule(sc: LiveSchedule) {
    setDraftName(sc.name);
    setDraftKind(sc.perspective ?? "discovery");
    setDraftGoal(sc.goal ?? "");
    setDraftMilestones(sc.milestones?.length ? sc.milestones.map((m) => m.title) : [""]);
    setDraftTemplateRef(sc.templateRef ?? "");

    const form = parseCron(sc.cron);
    if (form) {
      setDraftRepeat(true);
      setDraftFreq(form.freq);
      setDraftTimes(form.times);
      setDraftDows(form.dows);
      setDraftMonths(form.months);
      setCronOverride(null);
    } else {
      // Not representable here. Keep it verbatim so opening the schedule to
      // rename it cannot quietly change when it fires.
      setDraftTimes(["08:00"]);
      setDraftDows([]);
      setDraftMonths([]);
      setCronOverride(sc.cron);
    }
    setEditingId(sc.id);
    setNewSchedOpen(true);
  }

  async function submitSchedule() {
    const milestones = draftMilestones.map((m) => m.trim()).filter(Boolean);
    // Goal format: a goal must carry at least one milestone.
    if (draftGoal.trim() && milestones.length === 0) {
      setLiveError(t("daily.goalNeedsMilestone"));
      return;
    }
    // Compile the bound template (if any) into a runSpec the host agent fires.
    let templateLabel: string | undefined;
    let templateRef: string | undefined;
    let runSpec: unknown;
    const boundTemplateRef = draftTemplateRef || templateChoices[0]?.ref || "";
    if (boundTemplateRef) {
      // Everything the form collected, not just the goal — the milestones are
      // acceptance criteria the agents have to be told about.
      const task = scheduleTask({
        name: draftName.trim(),
        goal: draftGoal.trim(),
        milestones: milestones.map((title) => ({ title })),
      }) || t("daily.untitledSchedule");
      const c = compileRef(boundTemplateRef, tplStores, task);
      if (c && c.stages.length > 0) {
        templateLabel = c.label;
        templateRef = boundTemplateRef;
        // A schedule fires with nobody watching, so it may only use container
        // images approved for unattended runs.
        runSpec = buildRunSpec(c.stages, { unattended: true });
      }
    }
    if (live) {
      const existing = editingId ? liveSchedules.find((s) => s.id === editingId) : undefined;
      const spec = {
        name: draftName.trim() || t("daily.untitledSchedule"),
        cron: buildCron(),
        perspective: draftKind,
        // Editing must not silently re-enable a schedule the user paused; the
        // form has no active field, so the stored value carries through.
        active: existing ? existing.active : true,
        goal: draftGoal.trim(),
        // Editing preserves which milestones are already done: they are
        // progress, not configuration, and retyping the title should not undo
        // having ticked it off.
        milestones: milestones.map((title) => ({
          title,
          done: existing?.milestones?.find((m) => m.title === title)?.done ?? false,
        })),
        // A template that no longer compiles — its Solo was deleted, say —
        // yields nothing here. On create that is simply an unbound schedule; on
        // edit it would silently strip a binding the user did not touch, so the
        // stored one carries through instead.
        templateLabel: templateLabel ?? existing?.templateLabel,
        templateRef: templateRef ?? existing?.templateRef,
        runSpec: runSpec ?? existing?.runSpec,
      };
      try {
        if (editingId) {
          await schedulesApi.update(editingId, spec);
        } else {
          await schedulesApi.create(spec);
        }
        await refreshSchedules();
      } catch (e) {
        setLiveError(e instanceof Error ? e.message : String(e));
        return; // keep the modal open so the user can retry
      }
    }
    resetDraft();
    setNewSchedOpen(false);
  }

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
      {/* schedule sidebar */}
      <div style={{ width: 268, flex: "none", background: "var(--bg-panel)", borderRight: "1px solid var(--bd)", padding: "16px 13px", display: "flex", flexDirection: "column", gap: 16, overflowY: "auto" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>Schedules</span>
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{live ? liveSchedules.length : schedules.length}</span>
          <div onClick={() => setNewSchedOpen(true)} title={t("daily.newSchedule")} style={{ marginLeft: "auto", width: 22, height: 22, borderRadius: 6, background: "var(--tint-active)", border: "1px solid var(--tint-active-bd)", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--ac)", fontSize: 14, cursor: "pointer" }}>+</div>
        </div>

        {/* live schedule list (host agent) */}
        {live && (
          <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
            {liveSchedules.map((s) => {
              const kind = s.perspective ?? "discovery";
              return (
                <div key={s.id} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 10, padding: "11px 12px", display: "flex", flexDirection: "column", gap: 8, opacity: s.active ? 1 : 0.55 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                    <div style={{ width: 8, height: 8, borderRadius: 2, background: kindColor(kind) }} />
                    <span onClick={() => editSchedule(s)} title={t("common.edit")} style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)", cursor: "pointer" }}>{s.name}</span>
                    <div onClick={() => runScheduleNow(s.id)} title={t("daily.runNowTip")} style={{ marginLeft: "auto", cursor: runningNow ? "default" : "pointer", opacity: runningNow && runningNow !== s.id ? 0.4 : 1, font: "500 9.5px 'IBM Plex Mono'", color: "var(--ac)", padding: "0 4px" }}>
                      {runningNow === s.id ? t("daily.runningNow") : `▶ ${t("daily.runNow")}`}
                    </div>
                    <div onClick={() => editSchedule(s)} title={t("common.edit")} style={{ cursor: "pointer", font: "500 9.5px 'IBM Plex Mono'", color: "var(--ac)", padding: "0 4px" }}>{t("common.edit")}</div>
                    <div onClick={() => removeLiveSchedule(s.id)} title={t("common.delete")} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 13px 'IBM Plex Sans'", padding: "0 2px" }}>✕</div>
                  </div>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                    <span style={{ color: kindColor(kind) }}>{perspectiveLabel(kind)}</span>
                    <span>{s.cron}</span>
                    {s.templateLabel && (
                      <span title={t("daily.runsTemplateOnFire", { name: s.templateLabel })} style={{ marginLeft: "auto", font: "500 8.5px 'IBM Plex Mono'", color: "var(--ac)", flex: "none" }}>⚡{s.templateLabel}</span>
                    )}
                  </div>
                  <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                    <div onClick={() => toggleLiveSchedule(s.id)} title={t(s.active ? "daily.pause" : "daily.enable")} style={{ width: 30, height: 18, borderRadius: 9, background: s.active ? "var(--green)" : "var(--bd3)", position: "relative", cursor: "pointer", flex: "none", transition: "background .12s" }}>
                      <div style={{ position: "absolute", top: 2, left: s.active ? 14 : 2, width: 14, height: 14, borderRadius: "50%", background: "#fff", transition: "left .12s" }} />
                    </div>
                    <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: s.active ? "#67c9a4" : "var(--tx-faint)" }}>{t(s.active ? "daily.active" : "daily.paused")}</span>
                    <span style={{ marginLeft: "auto", font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.runCount", { count: s.runCount })}</span>
                  </div>
                  {s.goal && (
                    <div style={{ display: "flex", flexDirection: "column", gap: 3, marginTop: 2, paddingTop: 6, borderTop: "1px solid var(--bd)" }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 5 }}>
                        <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--ac)", flex: "none" }}>GOAL</span>
                        <span style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.goal}</span>
                        <span style={{ marginLeft: "auto", font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>
                          {s.milestones.filter((m) => m.done).length}/{s.milestones.length}
                        </span>
                      </div>
                      {s.milestones.map((m, i) => (
                        <div key={i} style={{ display: "flex", alignItems: "center", gap: 5, paddingLeft: 2 }}>
                          <span style={{ width: 5, height: 5, borderRadius: "50%", background: m.done ? "#5fbf95" : "var(--bd3)", flex: "none" }} />
                          <span style={{ font: "400 9px 'IBM Plex Sans'", color: m.done ? "var(--tx-faint)" : "var(--tx3)", textDecoration: m.done ? "line-through" : "none", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{m.title}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
            {liveSchedules.length === 0 && (
              <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "8px 2px" }}>{t("daily.noSchedules")}</div>
            )}
          </div>
        )}

        {/* external task sources (pull-model: Jira / Trello / Notion) */}
        {live && (
          <div style={{ display: "flex", flexDirection: "column", gap: 9, marginTop: 18 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
              <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)", letterSpacing: "0.5px" }}>{t("daily.externalTasks")}</span>
              <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
              <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.countUnit", { count: tickets.length })}</span>
            </div>

            {sources.length === 0 && (
              <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "2px 2px" }}>
                {t("daily.noTaskSources")}
              </div>
            )}
            {sources.length > 0 && (
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                {sources.map((src) => (
                  <div
                    key={src}
                    onClick={() => pulling === null && pullSource(src)}
                    title={t("daily.pullFrom", { src })}
                    style={{
                      display: "flex", alignItems: "center", gap: 6, cursor: pulling ? "default" : "pointer",
                      padding: "5px 10px", borderRadius: 7, border: "1px solid var(--bd2)", background: "var(--bg-card2)",
                      opacity: pulling && pulling !== src ? 0.5 : 1,
                    }}
                  >
                    <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx3)" }}>{src}</span>
                    <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--ac)" }}>{pulling === src ? t("daily.pulling") : t("daily.pull")}</span>
                  </div>
                ))}
              </div>
            )}

            {tickets.map((ticket) => (
              <div
                key={ticket.id}
                onClick={() => ticket.url && window.open(ticket.url, "_blank")}
                title={ticket.url || ticket.id}
                style={{
                  background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 10,
                  padding: "10px 12px", display: "flex", flexDirection: "column", gap: 6, cursor: ticket.url ? "pointer" : "default",
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                  <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{ticket.title}</span>
                </div>
                <div style={{ display: "flex", alignItems: "center", gap: 8, font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                  <span style={{ color: "var(--tx3)" }}>{ticket.source}</span>
                  <span style={{ color: ticketStateColor(ticket.state) }}>{ticket.state}</span>
                  <span
                    onClick={(e) => { e.stopPropagation(); if (promoting === null) promoteTicket(ticket); }}
                    title={t("daily.promoteTip")}
                    style={{ marginLeft: "auto", color: "var(--ac)", cursor: promoting ? "default" : "pointer", font: "600 9.5px 'IBM Plex Mono'" }}
                  >
                    {promoting === ticket.id ? t("daily.promoting") : t("daily.promote")}
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* schedule run history — executed while up / missed while down */}
        {live && (
          <div style={{ display: "flex", flexDirection: "column", gap: 7, marginTop: 18 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
              <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)", letterSpacing: "0.5px" }}>{t("daily.runHistory")}</span>
              <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
              <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.countUnit", { count: runs.length })}</span>
            </div>
            {runs.length === 0 && (
              <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "2px 2px" }}>{t("daily.noRuns")}</div>
            )}
            {runs.slice(0, 12).map((r) => {
              const st = runStatusStyle(r.status);
              const hasArtifacts = !!(r.outputDir || r.runId);
              return (
                <div
                  key={r.id}
                  onClick={() => hasArtifacts && setArtifactRun(r)}
                  title={hasArtifacts ? t("daily.showArtifacts") : undefined}
                  style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 10px", background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, cursor: hasArtifacts ? "pointer" : "default" }}
                >
                  <span style={{ width: 6, height: 6, borderRadius: "50%", background: st.color, flex: "none" }} />
                  <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r.name}</span>
                  {hasArtifacts && <span style={{ marginLeft: "auto", font: "500 9px 'IBM Plex Mono'", color: "var(--ac)", flex: "none" }}>{t("daily.artifacts")}</span>}
                  <span style={{ marginLeft: hasArtifacts ? 0 : "auto", font: "500 9px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>{shortWhen(r.scheduledAt)}</span>
                  <span style={{ font: "600 9px 'IBM Plex Mono'", color: st.color, flex: "none" }}>{st.label}</span>
                </div>
              );
            })}
          </div>
        )}

        {/* mock schedule list (default) */}
        {!live && (
        <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
          {schedules.map((s) => (
            <div key={s.name} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 10, padding: "11px 12px", display: "flex", flexDirection: "column", gap: 8 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                <div style={{ width: 8, height: 8, borderRadius: 2, background: kindColor(s.kind) }} />
                <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)" }}>{s.name}</span>
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 8, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                <span style={{ color: kindColor(s.kind) }}>{perspectiveLabel(s.kind)}</span>
                <span>{s.cron}</span>
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                <div style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--tx-faint)" }} />
                <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{s.pipeName}</span>
              </div>
              {s.review && <div style={{ font: "400 9.5px 'IBM Plex Mono'", color: "#d39a4e" }}>{s.review}</div>}
            </div>
          ))}
        </div>
        )}
      </div>

      {/* main column */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, background: "var(--bg-app)" }}>
        {/* header */}
        <div style={{ height: 48, flex: "none", display: "flex", alignItems: "center", padding: "0 20px", gap: 12, borderBottom: "1px solid var(--bd)" }}>
          {dailyIsGallery && <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("daily.galleryTitle")}</span>}
          {dailyIsCalendar && <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("daily.calendarTitle")}</span>}
          <div style={{ display: "flex", alignItems: "center", gap: 4, marginLeft: 4 }}>
            <div onClick={() => setDailyView("gallery")} style={segBtn(dailyIsGallery)}>{t("daily.gallery")}</div>
            <div onClick={() => setDailyView("calendar")} style={segBtn(dailyIsCalendar)}>{t("daily.calendar")}</div>
          </div>
          <div
            onClick={() => (live ? disconnectLive() : connectLive())}
            title={liveError ?? t(live ? "daily.hostConnected" : "daily.hostConnect")}
            style={{
              display: "flex", alignItems: "center", gap: 7, cursor: "pointer", marginLeft: 4,
              padding: "5px 11px", borderRadius: 7,
              border: `1px solid ${live ? "var(--tint-green-bd)" : liveError ? "var(--tint-red-bd)" : "var(--bd2)"}`,
              background: live ? "var(--tint-green)" : liveError ? "var(--tint-red)" : "var(--bg-card2)",
            }}
          >
            <span className={live ? "oc-active-dot" : undefined} style={{ width: 7, height: 7, borderRadius: "50%", background: live ? "var(--green)" : liveError ? "var(--red)" : "var(--tx-dim)" }} />
            <span style={{ font: "500 11px 'IBM Plex Mono'", color: live ? "#67c9a4" : liveError ? "var(--red)" : "var(--tx3)" }}>
              {connecting ? t("daily.connecting") : live ? "live · host agent" : "mock data"}
            </span>
          </div>
          <div style={{ flex: 1 }} />
          {dailyIsGallery && (
            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <div style={legendItem}><div style={legendDot("#7c5cff")} />video</div>
              <div style={legendItem}><div style={legendDot("#34d3e0")} />image</div>
              <div style={legendItem}><div style={legendDot("#5b9fe8")} />text</div>
              <div style={legendItem}><div style={legendDot("#e0a83e")} />voice</div>
            </div>
          )}
          {dailyIsCalendar && (
            <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
              <div style={legendItem}><div style={legendDot("#d39a4e")} />{t("daily.perspective.discovery")}</div>
              <div style={legendItem}><div style={legendDot("#5b9fe8")} />{t("daily.perspective.contextOpt")}</div>
              <div style={legendItem}><div style={legendDot("#5fbf95")} />{t("daily.perspective.automation")}</div>
            </div>
          )}
        </div>

        {/* gallery view — live shows what the schedules actually produced; the
            mock gallery below is the offline demo. */}
        {dailyIsGallery && live && <ArtifactGallery runs={runs} />}
        {dailyIsGallery && !live && (
          <div style={{ flex: 1, overflowY: "auto", padding: "18px 20px", display: "flex", flexDirection: "column", gap: 22 }}>
            {/* 新発見 */}
            <div>
              <div style={{ display: "flex", alignItems: "center", gap: 9, marginBottom: 12 }}>
                <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "#d39a4e", letterSpacing: "0.5px" }}>{t("daily.perspective.discovery")}</span>
                <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
                <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.countUnit", { count: 4 })}</span>
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 13 }}>
                {/* video */}
                <div onClick={() => openArtifact({ type: "video", title: "競合UIの動向まとめ", meta: "08:02 · Scout", grad: VIDEO_GRAD, duration: "1:24" })} style={cardBase}>
                  <div style={{ height: 96, background: VIDEO_GRAD, display: "flex", alignItems: "center", justifyContent: "center", position: "relative" }}>
                    <div style={{ width: 34, height: 34, borderRadius: "50%", background: "rgba(255,255,255,.12)", display: "flex", alignItems: "center", justifyContent: "center" }}>
                      <div style={{ width: 0, height: 0, borderLeft: "10px solid #fff", borderTop: "6px solid transparent", borderBottom: "6px solid transparent", marginLeft: 3 }} />
                    </div>
                    <span style={{ position: "absolute", right: 7, bottom: 7, font: "500 9px 'IBM Plex Mono'", color: "#cdb8ff", background: "rgba(0,0,0,.4)", padding: "2px 5px", borderRadius: 4 }}>1:24</span>
                    <span style={cornerDot("#7c5cff")} />
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>競合UIの動向まとめ</div><div style={cardSub}>08:02 · Scout</div></div>
                </div>
                {/* image */}
                <div onClick={() => openArtifact({ type: "image", title: "新着デザイン事例 12点", meta: "08:03 · Scout", imgCount: 12 })} style={cardBase}>
                  <div style={{ height: 96, background: IMAGE_GRAD, position: "relative" }}><span style={cornerDot("#34d3e0")} /></div>
                  <div style={cardMeta}><div style={cardTitle}>新着デザイン事例 12点</div><div style={cardSub}>08:03 · Scout</div></div>
                </div>
                {/* text */}
                <div onClick={() => openArtifact({ type: "text", title: "論文サマリ: RAG最新", meta: "08:05 · Scout" })} style={cardBase}>
                  <div style={{ height: 96, background: "var(--bg-thumb)", padding: 11, display: "flex", flexDirection: "column", gap: 4, position: "relative", overflow: "hidden" }}>
                    <span style={{ ...cornerDot("#5b9fe8"), zIndex: 1 }} />
                    <div style={skel("80%", 10)} /><div style={skel("95%")} /><div style={skel("70%")} /><div style={skel("88%")} />
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>論文サマリ: RAG最新</div><div style={cardSub}>08:05 · Scout</div></div>
                </div>
                {/* voice */}
                <div onClick={() => openArtifact({ type: "voice", title: "音声ブリーフィング", meta: "08:06 · Scout", grad: VOICE_GRAD, duration: "0:48" })} style={cardBase}>
                  <div style={{ height: 96, background: VOICE_GRAD, display: "flex", alignItems: "center", justifyContent: "center", gap: 3, position: "relative" }}>
                    <span style={cornerDot("#e0a83e")} />
                    {CARD_WAVE.map((h, i) => <div key={i} style={{ width: 3, height: h, background: "#e0a83e", borderRadius: 2 }} />)}
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>音声ブリーフィング</div><div style={cardSub}>08:06 · Scout · 0:48</div></div>
                </div>
              </div>
            </div>

            {/* 最適化 */}
            <div>
              <div style={{ display: "flex", alignItems: "center", gap: 9, marginBottom: 12 }}>
                <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "#5b9fe8", letterSpacing: "0.5px" }}>{t("daily.perspective.contextOpt")}</span>
                <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "#d39a4e", background: "var(--tint-amber)", padding: "2px 6px", borderRadius: 4 }}>{t("daily.hasReview")}</span>
                <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
                <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.countUnit", { count: 3 })}</span>
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 13 }}>
                <div onClick={() => openArtifact({ type: "text", title: "システムプロンプト改稿案", meta: "18:00 · Tuner · −24% tok" })} style={{ ...cardBase, background: "var(--tint-active)", border: "1px solid var(--tint-active-bd)" }}>
                  <div style={{ height: 96, background: "var(--bg-thumb)", padding: 11, display: "flex", flexDirection: "column", gap: 4, position: "relative" }}>
                    <span style={cornerDot("#5b9fe8")} />
                    <span style={{ position: "absolute", right: 7, top: 7, font: "500 8.5px 'IBM Plex Mono'", color: "var(--ac)", background: "var(--tint-accent)", padding: "2px 5px", borderRadius: 4 }}>{t("daily.needsReview")}</span>
                    <div style={skel("78%", 14)} /><div style={skel("90%")} /><div style={skel("62%")} />
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>システムプロンプト改稿案</div><div style={cardSub}>18:00 · Tuner · −24% tok</div></div>
                </div>
                <div onClick={() => openArtifact({ type: "text", title: "履歴要約スナップショット", meta: "18:01 · Tuner" })} style={cardBase}>
                  <div style={{ height: 96, background: "var(--bg-thumb)", padding: 11, display: "flex", flexDirection: "column", gap: 4, position: "relative" }}>
                    <span style={cornerDot("#5b9fe8")} />
                    <div style={skel("85%", 10)} /><div style={skel("70%")} /><div style={skel("92%")} /><div style={skel("55%")} />
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>履歴要約スナップショット</div><div style={cardSub}>18:01 · Tuner</div></div>
                </div>
                <div onClick={() => openArtifact({ type: "image", title: "RAGヒット率レポート", meta: "18:02 · Tuner", imgCount: 5 })} style={cardBase}>
                  <div style={{ height: 96, background: "var(--bg-thumb)", padding: 11, display: "flex", alignItems: "flex-end", gap: 4, position: "relative" }}>
                    <span style={cornerDot("#34d3e0")} />
                    {[40, 65, 48, 80, 30].map((h, i) => <div key={i} style={{ flex: 1, height: `${h}%`, background: i > 1 && i < 4 ? "#234a4f" : "#1d2f3a", borderRadius: 2 }} />)}
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>RAGヒット率レポート</div><div style={cardSub}>18:02 · Tuner</div></div>
                </div>
              </div>
            </div>

            {/* 自動化 */}
            <div>
              <div style={{ display: "flex", alignItems: "center", gap: 9, marginBottom: 12 }}>
                <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "#5fbf95", letterSpacing: "0.5px" }}>{t("daily.perspective.automation")}</span>
                <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
                <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.countUnit", { count: 4 })}</span>
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 13 }}>
                <div onClick={() => openArtifact({ type: "text", title: "日次レポート（配信済）", meta: "09:30 · Runner ✓" })} style={cardBase}>
                  <div style={{ height: 96, background: "var(--bg-thumb)", padding: 11, display: "flex", flexDirection: "column", gap: 4, position: "relative" }}>
                    <span style={cornerDot("#5b9fe8")} />
                    <div style={skel("88%", 10)} /><div style={skel("60%")} /><div style={skel("80%")} />
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>日次レポート（配信済）</div><div style={{ ...cardSub, color: "#5fbf95" }}>09:30 · Runner ✓</div></div>
                </div>
                <div onClick={() => openArtifact({ type: "voice", title: "議事録 音声要約", meta: "09:31 · Runner", grad: VOICE_GRAD, duration: "1:12" })} style={cardBase}>
                  <div style={{ height: 96, background: VOICE_GRAD, display: "flex", alignItems: "center", justifyContent: "center", gap: 3, position: "relative" }}>
                    <span style={cornerDot("#e0a83e")} />
                    {CARD_WAVE_2.map((h, i) => <div key={i} style={{ width: 3, height: h, background: "#e0a83e", borderRadius: 2 }} />)}
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>議事録 音声要約</div><div style={cardSub}>09:31 · Runner · 1:12</div></div>
                </div>
                <div onClick={() => openArtifact({ type: "image", title: "ダッシュボード スクショ", meta: "09:32 · Runner ✓", imgCount: 4 })} style={cardBase}>
                  <div style={{ height: 96, background: IMAGE_GRAD, position: "relative" }}><span style={cornerDot("#34d3e0")} /></div>
                  <div style={cardMeta}><div style={cardTitle}>ダッシュボード スクショ</div><div style={{ ...cardSub, color: "#5fbf95" }}>09:32 · Runner ✓</div></div>
                </div>
                <div onClick={() => openArtifact({ type: "video", title: "操作リプレイ動画", meta: "09:33 · Runner ✓", grad: VIDEO_GRAD, duration: "0:36" })} style={cardBase}>
                  <div style={{ height: 96, background: VIDEO_GRAD, display: "flex", alignItems: "center", justifyContent: "center", position: "relative" }}>
                    <div style={{ width: 34, height: 34, borderRadius: "50%", background: "rgba(255,255,255,.12)", display: "flex", alignItems: "center", justifyContent: "center" }}>
                      <div style={{ width: 0, height: 0, borderLeft: "10px solid #fff", borderTop: "6px solid transparent", borderBottom: "6px solid transparent", marginLeft: 3 }} />
                    </div>
                    <span style={cornerDot("#7c5cff")} />
                  </div>
                  <div style={cardMeta}><div style={cardTitle}>操作リプレイ動画</div><div style={{ ...cardSub, color: "#5fbf95" }}>09:33 · Runner ✓</div></div>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* calendar view */}
        {dailyIsCalendar && (
          <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
            {/* calendar grid */}
            <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, padding: "16px 18px" }}>
              {/* toolbar */}
              <div style={{ flex: "none", display: "flex", alignItems: "center", gap: 11, marginBottom: 14 }}>
                <div onClick={() => stepCalendar(-1)} style={{ width: 28, height: 28, borderRadius: 7, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", color: "var(--tx3)", fontSize: 13 }}>◀</div>
                <span style={{ font: "700 16px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.3px", minWidth: 128 }}>{calLabel}</span>
                <div onClick={() => stepCalendar(1)} style={{ width: 28, height: 28, borderRadius: 7, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", color: "var(--tx3)", fontSize: 13 }}>▶</div>
                <div onClick={() => { const t = startOfToday(); setToday(t); setCursor(t); setSelectedDate(t); }} style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx2)", padding: "6px 12px", border: "1px solid var(--bd2)", borderRadius: 7, background: "var(--bg-card2)", cursor: "pointer" }}>{t("daily.today")}</div>
                <div style={{ display: "flex", gap: 3, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: 2, marginLeft: 4 }}>
                  <div onClick={() => setCalMode("month")} style={calModeBtn(calMode === "month")}>{t("daily.month")}</div>
                  <div onClick={() => setCalMode("week")} style={calModeBtn(calMode === "week")}>{t("daily.week")}</div>
                  <div onClick={() => setCalMode("day")} style={calModeBtn(calMode === "day")}>{t("daily.day")}</div>
                </div>
                <div style={{ flex: 1 }} />
                <div onClick={() => setNewSchedOpen(true)} style={{ display: "flex", alignItems: "center", gap: 6, font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "7px 13px", borderRadius: 7, cursor: "pointer" }}>＋ {t("daily.newSchedule")}</div>
              </div>

              {/* MONTH view */}
              {calMode === "month" && (
                <>
                  <div style={{ flex: "none", display: "grid", gridTemplateColumns: "repeat(7,minmax(0,1fr))", borderLeft: "1px solid var(--bd)", borderTop: "1px solid var(--bd)" }}>
                    {weekdays.map((w, i) => (
                      <div key={w} style={{ padding: "7px 0", textAlign: "center", font: "600 10px 'IBM Plex Mono'", color: i === 0 ? "#e0654e" : i === 6 ? "#5b9fe8" : "var(--tx3)", borderRight: "1px solid var(--bd)", borderBottom: "1px solid var(--bd)", background: "var(--bg-panel)" }}>{w}</div>
                    ))}
                  </div>
                  <div style={{ flex: 1, display: "grid", gridTemplateColumns: "repeat(7,minmax(0,1fr))", gridAutoRows: "1fr", borderLeft: "1px solid var(--bd)", overflowY: "auto" }}>
                    {calCells.map((d, i) => {
                      const inMonth = d.getMonth() === month;
                      const isToday = sameDay(d, today);
                      const isSel = sameDay(d, selectedDate);
                      const runs = inMonth ? runsForDate(d) : [];
                      const shown = runs.slice(0, 2);
                      const more = runs.length - shown.length;
                      return (
                        <div key={i} onClick={() => inMonth && setSelectedDate(d)} style={{ borderRight: "1px solid var(--bd)", borderBottom: "1px solid var(--bd)", padding: "5px 5px", display: "flex", flexDirection: "column", gap: 3, minHeight: 0, cursor: inMonth ? "pointer" : "default", overflow: "hidden", background: isSel && inMonth ? "var(--tint-active)" : "transparent" }}>
                          {inMonth && (
                            <>
                              <div style={isToday ? { alignSelf: "flex-start", minWidth: 18, height: 18, padding: "0 5px", borderRadius: 9, background: "var(--ac)", color: "#06121e", font: "600 10.5px 'IBM Plex Mono'", display: "flex", alignItems: "center", justifyContent: "center" } : { font: "600 10.5px 'IBM Plex Mono'", color: "var(--tx2)", padding: "0 2px" }}>{d.getDate()}</div>
                              {shown.map((r, j) => (
                                <div
                                  key={j}
                                  onClick={(e) => { if (r.run) { e.stopPropagation(); setArtifactRun(r.run); } }}
                                  title={r.run ? t("daily.showArtifacts") : undefined}
                                  style={{ display: "flex", alignItems: "center", gap: 4, padding: "1px 4px", borderRadius: 4, background: "var(--bg-card2)", overflow: "hidden", cursor: r.run ? "pointer" : "inherit", opacity: r.status ? 1 : 0.72 }}
                                >
                                  <div style={{ width: 5, height: 5, borderRadius: "50%", background: perspectiveColor(r.perspective), flex: "none" }} />
                                  <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: runStateLabel(r).color, flex: "none" }}>{r.time}</span>
                                  <span style={{ font: "500 9px 'IBM Plex Sans'", color: "var(--tx2)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{r.name}</span>
                                </div>
                              ))}
                              {more > 0 && <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", paddingLeft: 3 }}>{t("daily.andMore", { count: more })}</span>}
                            </>
                          )}
                        </div>
                      );
                    })}
                  </div>
                </>
              )}

              {/* WEEK view */}
              {calMode === "week" && (
                <>
                  <div style={{ flex: "none", font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)", marginBottom: 10 }}>{calWeekLabel}</div>
                  <div style={{ flex: 1, display: "grid", gridTemplateColumns: "repeat(7,minmax(0,1fr))", borderTop: "1px solid var(--bd)", borderLeft: "1px solid var(--bd)", borderBottom: "1px solid var(--bd)", overflow: "hidden", minHeight: 0 }}>
                    {weekDays.map((d, i) => {
                      const isSel = sameDay(d, selectedDate);
                      const isToday = sameDay(d, today);
                      const runs = runsForDate(d);
                      return (
                        <div key={i} onClick={() => setSelectedDate(d)} style={{ borderRight: "1px solid var(--bd)", padding: "10px 7px", display: "flex", flexDirection: "column", gap: 9, minHeight: 0, cursor: "pointer", background: isSel ? "var(--tint-active)" : "transparent" }}>
                          <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 3 }}>
                            <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{weekdays[d.getDay()]}</span>
                            <div style={isToday ? { width: 22, height: 22, borderRadius: "50%", background: "var(--ac)", color: "#06121e", font: "600 11px 'IBM Plex Mono'", display: "flex", alignItems: "center", justifyContent: "center" } : { font: "600 12px 'IBM Plex Mono'", color: "var(--tx2)" }}>{d.getDate()}</div>
                          </div>
                          <div style={{ display: "flex", flexDirection: "column", gap: 5, overflowY: "auto" }}>
                            {runs.map((r, j) => (
                              <div
                                key={j}
                                onClick={(e) => { if (r.run) { e.stopPropagation(); setArtifactRun(r.run); } }}
                                title={r.run ? t("daily.showArtifacts") : undefined}
                                style={{ display: "flex", alignItems: "center", gap: 4, padding: "2px 5px", borderRadius: 4, background: "var(--bg-card2)", overflow: "hidden", cursor: r.run ? "pointer" : "inherit", opacity: r.status ? 1 : 0.72 }}
                              >
                                <div style={{ width: 5, height: 5, borderRadius: "50%", background: perspectiveColor(r.perspective), flex: "none" }} />
                                <span style={{ font: "500 8px 'IBM Plex Mono'", color: runStateLabel(r).color, flex: "none" }}>{r.time}</span>
                                <span style={{ font: "500 8.5px 'IBM Plex Sans'", color: "var(--tx2)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{r.name}</span>
                              </div>
                            ))}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </>
              )}

              {/* DAY view */}
              {calMode === "day" && (
                <>
                  <div style={{ flex: "none", font: "600 13px 'IBM Plex Sans'", color: "var(--tx)", marginBottom: 10 }}>{calDayLabel}</div>
                  <div style={{ flex: 1, overflowY: "auto", minHeight: 0 }}>
                    {Array.from({ length: 24 }, (_, h) => {
                      const runs = runsForDate(cursor).filter((r) => parseInt(r.time.slice(0, 2), 10) === h);
                      return (
                        <div key={h} style={{ display: "flex", gap: 12, padding: "8px 0", borderBottom: "1px solid var(--bd)" }}>
                          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-faint)", width: 42, flex: "none", paddingTop: 2 }}>{pad2(h)}:00</span>
                          <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: 6 }}>
                            {runs.map((r, j) => (
                              <div
                                key={j}
                                onClick={() => r.run && setArtifactRun(r.run)}
                                title={r.run ? t("daily.showArtifacts") : undefined}
                                style={{ display: "flex", alignItems: "center", gap: 8, padding: "8px 11px", borderRadius: 8, background: "var(--bg-card2)", border: "1px solid var(--bd2)", cursor: r.run ? "pointer" : "default", opacity: r.status ? 1 : 0.78 }}
                              >
                                <div style={{ width: 8, height: 8, borderRadius: 2, background: perspectiveColor(r.perspective), flex: "none" }} />
                                <span style={{ font: "600 9px 'IBM Plex Mono'", color: perspectiveColor(r.perspective) }}>{perspectiveLabel(r.perspective)}</span>
                                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{r.name}</span>
                                <span style={{ font: "600 9px 'IBM Plex Mono'", color: runStateLabel(r).color }}>{runStateLabel(r).label}</span>
                                {r.run && <span style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--ac)" }}>{t("daily.artifacts")}</span>}
                                <span style={{ marginLeft: "auto", font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{r.time}</span>
                              </div>
                            ))}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </>
              )}
            </div>

            {/* selected day panel */}
            <div style={{ width: 280, flex: "none", borderLeft: "1px solid var(--bd)", background: "var(--bg-panel)", display: "flex", flexDirection: "column", minHeight: 0 }}>
              <div style={{ flex: "none", padding: "16px 18px 12px", borderBottom: "1px solid var(--bd)" }}>
                <div style={{ font: "700 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{calSelLabel}</div>
                <div style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)", marginTop: 4 }}>
                  {selRuns.length === 0 ? t("daily.noRunsShort") : selRuns.some((r) => r.status) ? t("daily.countUnit", { count: selRuns.length }) : t("daily.plannedCount", { count: selRuns.length })}
                </div>
              </div>
              <div style={{ flex: 1, overflowY: "auto", padding: "14px 16px", display: "flex", flexDirection: "column", gap: 9 }}>
                {selRuns.map((r, i) => (
                  <div key={i} style={{ display: "flex", gap: 11, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 10, padding: "11px 12px" }}>
                    <div style={{ width: 3, alignSelf: "stretch", borderRadius: 2, background: perspectiveColor(r.perspective), flex: "none" }} />
                    <div style={{ display: "flex", flexDirection: "column", gap: 6, minWidth: 0, flex: 1 }}>
                      <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{r.name}</span>
                      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                        <span style={{ font: "600 9px 'IBM Plex Mono'", color: perspectiveColor(r.perspective) }}>{perspectiveLabel(r.perspective)}</span>
                        <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{r.time}</span>
                        <span style={{ font: "600 9px 'IBM Plex Mono'", color: runStateLabel(r).color }}>{runStateLabel(r).label}</span>
                      </div>
                      {/* Only actions that do something: a finished run opens
                          its artifacts, a planned one can be paused. */}
                      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                        {r.run && (r.run.outputDir || r.run.runId) && (
                          <div onClick={() => setArtifactRun(r.run!)} style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--ac)", padding: "4px 9px", borderRadius: 6, border: "1px solid var(--tint-active-bd)", background: "var(--tint-active)", cursor: "pointer", flex: "none" }}>{t("daily.artifacts")}</div>
                        )}
                        {live && !r.status && r.scheduleId.startsWith("sch-") && (
                          <div onClick={() => toggleLiveSchedule(r.scheduleId)} style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx3)", padding: "4px 9px", borderRadius: 6, border: "1px solid var(--bd2)", background: "var(--bg-card)", cursor: "pointer", flex: "none" }}>{t("daily.pause")}</div>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
                {selRuns.length === 0 && (
                  <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 10, padding: "30px 0", color: "var(--tx-faint)" }}>
                    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="var(--tx-faint)" strokeWidth="1.4"><rect x="3" y="5" width="18" height="16" rx="2" /><path d="M3 9h18M8 3v4M16 3v4" /></svg>
                    <span style={{ font: "400 11px 'IBM Plex Sans'" }}>
                      {t(live && startOfDayMs(selectedDate) < startOfDayMs(today) ? "daily.noRunsThatDay" : "daily.noPlannedThatDay")}
                    </span>
                  </div>
                )}
              </div>
            </div>
          </div>
        )}
      </div>

      {/* new schedule modal */}
      {newSchedOpen && (
        <div onClick={() => { setNewSchedOpen(false); resetDraft(); }} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 40 }}>
          <div onClick={stop} style={{ width: 460, maxHeight: "90%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, boxShadow: "0 24px 70px rgba(0,0,0,.45)", display: "flex", flexDirection: "column", overflow: "hidden" }}>
            <div style={{ padding: "18px 22px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10 }}>
              <span style={{ font: "700 16px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.2px" }}>{t(editingId ? "daily.editSchedule" : "daily.newSchedule")}</span>
              {editingId && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{editingId}</span>}
              <div style={{ flex: 1 }} />
              <div onClick={() => { setNewSchedOpen(false); resetDraft(); }} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 18px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
            </div>
            <div style={{ padding: "20px 22px", display: "flex", flexDirection: "column", gap: 18, overflowY: "auto" }}>
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.taskName")}</span>
                <input value={draftName} onChange={(e) => setDraftName(e.target.value)} placeholder={t("daily.taskNamePlaceholder")} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "10px 12px", font: "500 13px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }} />
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                  <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.goal")}</span>
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.goalHint")}</span>
                </div>
                <input value={draftGoal} onChange={(e) => setDraftGoal(e.target.value)} placeholder={t("daily.goalPlaceholder")} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "10px 12px", font: "500 13px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }} />
                {draftGoal.trim() && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 2 }}>
                    <span style={{ font: "600 10px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t("daily.milestones")}</span>
                    {draftMilestones.map((m, i) => (
                      <div key={i} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                        <span style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none", width: 16 }}>{i + 1}</span>
                        <input
                          value={m}
                          onChange={(e) => setDraftMilestones((ms) => ms.map((x, j) => (j === i ? e.target.value : x)))}
                          placeholder={t("daily.milestoneN", { n: i + 1 })}
                          style={{ flex: 1, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "7px 10px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }}
                        />
                        {draftMilestones.length > 1 && (
                          <div onClick={() => setDraftMilestones((ms) => ms.filter((_, j) => j !== i))} title={t("common.delete")} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 14px 'IBM Plex Sans'", padding: "0 4px", flex: "none" }}>✕</div>
                        )}
                      </div>
                    ))}
                    <div onClick={() => setDraftMilestones((ms) => [...ms, ""])} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "2px 2px" }}>＋ {t("daily.addMilestone")}</div>
                  </div>
                )}
              </div>
              {true && (
                <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                    <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.agentTemplate")}</span>
                    <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.agentTemplateHint")}</span>
                  </div>
                  <select
                    value={draftTemplateRef || templateChoices[0]?.ref || ""}
                    onChange={(e) => setDraftTemplateRef(e.target.value)}
                    style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "10px 12px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }}
                  >
                    {/* A single agent is a one-stage template, so it appears in
                        this list like any other. There is deliberately no empty
                        "default" option: an unbound schedule used to fall through
                        to a bare container that ignored every template setting. */}
                    {templateChoices.map((o) => (
                      <option key={o.ref} value={o.ref}>{o.label}</option>
                    ))}
                  </select>
                </div>
              )}
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.perspectiveLabel")}</span>
                <div style={{ display: "flex", gap: 8 }}>
                  {PERSPECTIVES.map((k) => (
                    <div key={k} onClick={() => setDraftKind(k)} style={{ ...pill(draftKind === k), flex: 1 }}>{perspectiveLabel(k)}</div>
                  ))}
                </div>
              </div>
              <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
                  <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.repeat")}</span>
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.repeatHint")}</span>
                  <div onClick={() => setDraftRepeat((v) => !v)} style={{ marginLeft: "auto", width: 38, height: 22, borderRadius: 11, background: draftRepeat ? "var(--ac)" : "var(--bd3)", position: "relative", cursor: "pointer", transition: "background .12s" }}>
                    <div style={{ position: "absolute", top: 2, left: draftRepeat ? 18 : 2, width: 18, height: 18, borderRadius: "50%", background: "#fff", transition: "left .12s" }} />
                  </div>
                </div>
                {draftShowFreq && (
                  <div style={{ display: "flex", gap: 8 }}>
                    {(["daily", "weekly", "monthly"] as const).map((f) => (
                      <div key={f} onClick={() => setDraftFreq(f)} style={{ ...pill(draftFreq === f), flex: 1 }}>{t(`daily.freq.${f}`)}</div>
                    ))}
                  </div>
                )}
              </div>

              {draftShowMonth && (
                <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                  <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.whichMonths")} <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.multiple")}</span></span>
                  <div style={{ display: "grid", gridTemplateColumns: "repeat(6,1fr)", gap: 6 }}>
                    {Array.from({ length: 12 }, (_, i) => i + 1).map((m) => (
                      <div key={m} onClick={() => setDraftMonths((a) => toggleIn(a, m))} style={smallToggle(draftMonths.includes(m))}>{monthName(m - 1)}</div>
                    ))}
                  </div>
                </div>
              )}

              {draftShowWeek && (
                <>
                  <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                    <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.whichWeeks")} <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.multiple")}</span></span>
                    <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                      {[0, 1, 2, 3, 4].map((w) => (
                        <div key={w} onClick={() => setDraftWeeks((a) => toggleIn(a, w))} style={smallToggle(draftWeeks.includes(w))}>{t("daily.recur.nthWeek", { n: w + 1 })}</div>
                      ))}
                    </div>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                    <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.whichWeekdays")} <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.multiple")}</span></span>
                    <div style={{ display: "flex", gap: 6 }}>
                      {weekdays.map((w, i) => (
                        <div key={w} onClick={() => setDraftDows((a) => toggleIn(a, i))} style={{ ...smallToggle(draftDows.includes(i)), flex: 1, textAlign: "center" }}>{w}</div>
                      ))}
                    </div>
                  </div>
                </>
              )}

              {/* 実行時刻 */}
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.runTimes")} <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.multipleAdd")}</span></span>
                <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
                  {draftTimes.map((t, i) => (
                    <div key={i} style={{ display: "flex", alignItems: "center", gap: 9 }}>
                      <input type="time" value={t} onChange={(e) => setDraftTimes((arr) => arr.map((x, j) => (j === i ? e.target.value : x)))} style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "600 12px 'IBM Plex Mono'", color: "var(--tx)", outline: "none", colorScheme: "dark" }} />
                      {draftTimes.length > 1 && (
                        <div onClick={() => setDraftTimes((arr) => arr.filter((_, j) => j !== i))} style={{ width: 28, height: 28, flex: "none", borderRadius: 7, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", color: "var(--tx-dim)", fontSize: 14 }}>✕</div>
                      )}
                    </div>
                  ))}
                </div>
                <div onClick={() => setDraftTimes((arr) => [...arr, "12:00"])} style={{ alignSelf: "flex-start", display: "flex", alignItems: "center", gap: 6, font: "600 10.5px 'IBM Plex Sans'", color: "var(--ac)", padding: "6px 12px", border: "1px dashed var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: "pointer" }}>＋ {t("daily.addTime")}</div>
                {cronOverride && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 6, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 11px" }}>
                    <span style={{ font: "400 10px 'IBM Plex Mono'", color: "#d39a4e", lineHeight: 1.6 }}>
                      {t("daily.cronUnrepresentable")}
                      <span style={{ color: "var(--tx3)" }}> {cronOverride}</span>
                    </span>
                    <div onClick={() => setCronOverride(null)} style={{ alignSelf: "flex-start", font: "600 10px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 10px", border: "1px solid var(--tint-active-bd)", borderRadius: 6, background: "var(--tint-active)", cursor: "pointer" }}>
                      {t("daily.rebuildFromForm")}
                    </div>
                  </div>
                )}
                {droppedTimes.length > 0 && (
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "#d39a4e", lineHeight: 1.6 }}>
                    {t("daily.mixedMinutes", { times: droppedTimes.join(" / ") })}
                  </span>
                )}
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>cron: {buildCron()}</span>
              </div>

              <div style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 8, padding: "10px 12px" }}>
                <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>{t("daily.execution")}</span>
                <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx3)" }}>{recurSummary}</span>
              </div>
            </div>
            <div style={{ padding: "16px 22px", borderTop: "1px solid var(--bd)", display: "flex", gap: 9, justifyContent: "flex-end" }}>
              <div onClick={() => { setNewSchedOpen(false); resetDraft(); }} style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "9px 16px", borderRadius: 8, cursor: "pointer" }}>{t("common.cancel")}</div>
              <div onClick={submitSchedule} style={{ font: "600 12px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "9px 18px", borderRadius: 8, cursor: "pointer" }}>{t(editingId ? "common.save" : "common.create")}</div>
            </div>
          </div>
        </div>
      )}

      {/* scheduled-run artifacts (produced files + agent logs) */}
      {artifactRun && (
        <DailyRunDrawer
          run={artifactRun}
          onClose={() => setArtifactRun(null)}
          onOptimize={artifactRun.template ? () => { setOptimizeRun(artifactRun); setArtifactRun(null); } : undefined}
        />
      )}

      {/* optimization loop: edit the used template's per-granularity prompt */}
      {optimizeRun && (() => {
        const sc = liveSchedules.find((s) => s.id === optimizeRun.scheduleId);
        if (!sc || !sc.templateRef) return null;
        return (
          <RunOptimizer
            templateRef={sc.templateRef}
            templateLabel={sc.templateLabel}
            onClose={() => setOptimizeRun(null)}
            onSynced={() => { refreshSchedules(); refreshRuns(); }}
          />
        );
      })()}

      {/* artifact preview modal */}
      {artifact && (
        <div onClick={closeArtifact} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.62)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: 32 }}>
          <div onClick={stop} style={{ width: 820, maxWidth: "100%", maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, boxShadow: "0 24px 80px rgba(0,0,0,.5)", display: "flex", flexDirection: "column", overflow: "hidden" }}>
            {/* header */}
            <div style={{ padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 11, flex: "none" }}>
              <div style={{ width: 9, height: 9, borderRadius: 2, background: "var(--ac)" }} />
              <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.2px" }}>{artifact.title}</span>
                <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{artifact.meta}</span>
              </div>
              <div style={{ flex: 1 }} />
              <div onClick={closeArtifact} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
            </div>

            {/* MEDIA (video / voice) */}
            {(artifact.type === "video" || artifact.type === "voice") && (
              <div style={{ display: "flex", flexDirection: "column" }}>
                {artifact.type === "video" && (
                  <div style={{ height: 400, background: artifact.grad, display: "flex", alignItems: "center", justifyContent: "center", position: "relative" }}>
                    <div onClick={() => setPlaying((v) => !v)} style={{ width: 64, height: 64, borderRadius: "50%", background: "rgba(255,255,255,.92)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", boxShadow: "0 6px 24px rgba(0,0,0,.35)", color: "#1c1530", font: "600 20px 'IBM Plex Sans'" }}>{playing ? "❚❚" : "▶"}</div>
                  </div>
                )}
                {artifact.type === "voice" && (
                  <div style={{ height: 240, background: VOICE_GRAD, display: "flex", alignItems: "center", justifyContent: "center", gap: 2, padding: "0 40px" }}>
                    {MODAL_WAVE.map((h, i) => <div key={i} style={{ width: 4, height: h, borderRadius: 2, background: "#e0a83e" }} />)}
                  </div>
                )}
                <div style={{ padding: "16px 22px", display: "flex", alignItems: "center", gap: 14, borderTop: "1px solid var(--bd)", background: "var(--bg-panel)" }}>
                  <div onClick={() => setPlaying((v) => !v)} style={{ width: 38, height: 38, borderRadius: "50%", background: "var(--ac)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", flex: "none", color: "#06121e", font: "600 14px 'IBM Plex Sans'" }}>{playing ? "❚❚" : "▶"}</div>
                  <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx3)", width: 40, textAlign: "right" }}>0:00</span>
                  <div style={{ flex: 1, height: 6, background: "var(--skel)", borderRadius: 3, position: "relative", cursor: "pointer" }}>
                    <div style={{ position: "absolute", left: 0, top: 0, bottom: 0, width: playing ? "34%" : "0%", background: "linear-gradient(90deg,#34d3e0,#4f9dff)", borderRadius: 3, transition: "width .2s" }} />
                    <div style={{ position: "absolute", left: playing ? "34%" : "0%", top: "50%", transform: "translate(-50%,-50%)", width: 12, height: 12, borderRadius: "50%", background: "#fff", boxShadow: "0 1px 4px rgba(0,0,0,.4)", transition: "left .2s" }} />
                  </div>
                  <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 40 }}>{artifact.duration}</span>
                </div>
              </div>
            )}

            {/* IMAGE */}
            {artifact.type === "image" && (
              <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
                <div style={{ padding: "12px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 9, flex: "none" }}>
                  <span style={{ font: "400 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("daily.imageCount", { count: artifact.imgCount })}</span>
                  <div style={{ flex: 1 }} />
                  <div onClick={() => setImgMode("grid")} style={smallToggle(imgMode === "grid")}>{t("daily.grid")}</div>
                  <div onClick={() => setImgMode("h")} style={smallToggle(imgMode === "h")}>{t("daily.scrollH")}</div>
                  <div onClick={() => setImgMode("v")} style={smallToggle(imgMode === "v")}>{t("daily.scrollV")}</div>
                </div>
                <div style={{ padding: "18px 20px", height: 440, overflow: "auto" }}>
                  <div style={imgMode === "grid"
                    ? { display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 12 }
                    : imgMode === "h"
                      ? { display: "flex", gap: 12, overflowX: "auto", height: "100%" }
                      : { display: "flex", flexDirection: "column", gap: 12 }}>
                    {Array.from({ length: artifact.imgCount ?? 4 }, (_, i) => (
                      <div key={i} style={{ background: IMAGE_GRAD, border: "1px solid var(--bd2)", borderRadius: 8, height: imgMode === "v" ? 200 : imgMode === "h" ? "100%" : 120, minWidth: imgMode === "h" ? 220 : 0, flex: imgMode === "h" ? "none" : undefined }} />
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* TEXT (markdown) */}
            {artifact.type === "text" && (
              <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
                <div style={{ padding: "12px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 9, flex: "none" }}>
                  <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>FORMAT: MARKDOWN</span>
                  <div style={{ flex: 1 }} />
                  <div onClick={() => setTextMode("rendered")} style={smallToggle(textMode === "rendered")}>{t("daily.preview")}</div>
                  <div onClick={() => setTextMode("raw")} style={smallToggle(textMode === "raw")}>{t("daily.source")}</div>
                </div>
                <div style={{ height: 440, overflowY: "auto", padding: "22px 28px" }}>
                  {textMode === "rendered" ? (
                    <div style={{ fontFamily: "'IBM Plex Sans'", fontSize: 13.5, maxWidth: 640, color: "var(--tx)" }}>
                      <h1 style={{ font: "700 22px 'IBM Plex Sans'", margin: "0 0 14px" }}>論文サマリ: RAG最新</h1>
                      <h2 style={{ font: "600 15px 'IBM Plex Sans'", color: "var(--tx2)", margin: "18px 0 8px" }}>概要</h2>
                      <ul style={{ margin: "0 0 8px", paddingLeft: 20, color: "var(--tx2)", lineHeight: 1.7 }}>
                        <li>Retrieval-Augmented Generation の最新手法を 3 本の論文から要約。</li>
                        <li>ハイブリッド検索 + 再ランクで <strong>ヒット率 +18%</strong>。</li>
                      </ul>
                      <h2 style={{ font: "600 15px 'IBM Plex Sans'", color: "var(--tx2)", margin: "18px 0 8px" }}>手法</h2>
                      <ol style={{ margin: "0 0 8px", paddingLeft: 20, color: "var(--tx2)", lineHeight: 1.7 }}>
                        <li>Dense + Sparse のハイブリッド検索</li>
                        <li>Cross-encoder による再ランク</li>
                        <li>クエリ書き換え (HyDE)</li>
                      </ol>
                      <blockquote style={{ margin: "14px 0 0", padding: "8px 14px", borderLeft: "3px solid var(--ac)", color: "var(--tx3)", background: "var(--bg-inset)" }}>圧縮により入力トークンを 24% 削減。</blockquote>
                    </div>
                  ) : (
                    <pre style={{ fontFamily: "'IBM Plex Mono',monospace", fontSize: 12.5, lineHeight: 1.7, color: "var(--tx2)", whiteSpace: "pre-wrap", margin: 0 }}>{RAW_MD}</pre>
                  )}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
