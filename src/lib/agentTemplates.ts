// Agent templates, at every granularity.
import i18n from "@/i18n";
//
// A "single agent" is not a separate concept from a multi-agent template — it is
// a template with one stage. Everything here therefore speaks one vocabulary: a
// template ref ("solo:<id>" | "static:<id>" | the Dynamic sentinel) compiles to
// a Stage DAG that runs through the orchestrator. Delivery and Daily both bind
// templates this way, which is why this lives in lib rather than under either
// screen.
import { compileGraph, compileSupervisor, compileSolo } from "@/features/delivery/review/compileTemplate";
import type { RunStage } from "@/lib/sandbox";
import type { SoloAgent, StaticTemplate } from "@/lib/templates";
import type { ProviderInput } from "@/lib/providers";
import type { ToolDef } from "@/lib/tools";
import type { HistorySettings } from "@/lib/chatContext";

export interface TemplateStores {
  solos: SoloAgent[];
  staticTpls: StaticTemplate[];
  providers: ProviderInput[];
  tools: ToolDef[];
  /** In-stage history compaction (Settings › 履歴圧縮). Optional: a caller that
   *  omits it compiles stages with no compaction fields, leaving the agent on
   *  its built-in defaults. */
  history?: HistorySettings;
}

export interface TemplateOption {
  ref: string; // "solo:<id>" | "static:<id>" | DYNAMIC_REF
  label: string;
  sub: string;
}

/** Sentinel ref: pick the template at run time via the router stage. Only
 *  bindable where a router can run (Delivery), not on a schedule. */
export const DYNAMIC_REF = "dynamic";

function staticLabel(t: StaticTemplate): string {
  return `${t.pattern === "graph" ? "Graph" : "Supervisor"} — ${t.name}`;
}

function staticSub(t: StaticTemplate): string {
  return t.pattern === "graph"
    ? i18n.t("templates.sub.graph", { count: t.nodes.length })
    : i18n.t("templates.sub.supervisor", { count: t.workers.length + 1 });
}

/** All bindable templates, single-agent and multi-agent alike. */
export function templateOptions(st: TemplateStores, opts?: { includeDynamic?: boolean }): TemplateOption[] {
  const out: TemplateOption[] = [
    ...st.solos.map((s) => ({ ref: `solo:${s.id}`, label: `Solo — ${s.name}`, sub: i18n.t("templates.sub.solo", { role: s.role }) })),
    ...st.staticTpls.map((t) => ({ ref: `static:${t.id}`, label: staticLabel(t), sub: staticSub(t) })),
  ];
  if (opts?.includeDynamic) {
    out.push({ ref: DYNAMIC_REF, label: i18n.t("review.dynamicAuto"), sub: i18n.t("templates.sub.dynamic") });
  }
  return out;
}

/** Resolve a stored ref against the current stores, tolerating older values.
 *
 *  Assignments were briefly stored as a bare static-template id, and "" meant
 *  "the built-in single agent" — a shape that no longer exists now that a single
 *  agent is just a one-stage template. Both are mapped onto a real ref so stored
 *  assignments keep resolving; "" falls back to the first available template. */
export function normalizeRef(ref: string, st: TemplateStores): string {
  const concrete = templateOptions(st); // no Dynamic: it needs templates to pick from
  if (ref === DYNAMIC_REF || ref === "__dynamic__") {
    // Dynamic is only meaningful with something to route to.
    return concrete.length > 0 ? DYNAMIC_REF : "";
  }
  if (concrete.some((o) => o.ref === ref)) return ref;
  if (ref && st.staticTpls.some((t) => t.id === ref)) return `static:${ref}`;
  return concrete[0]?.ref ?? "";
}

/** Compile a template ref into stages + a display label (null if unresolved). */
export function compileRef(ref: string, st: TemplateStores, task: string): { label: string; stages: RunStage[] } | null {
  const [kind, id] = ref.split(":");
  if (kind === "solo") {
    const solo = st.solos.find((s) => s.id === id);
    if (!solo) return null;
    return { label: `Solo — ${solo.name}`, stages: compileSolo(solo, st.providers, st.tools, task, st.history) };
  }
  if (kind === "static") {
    const t = st.staticTpls.find((x) => x.id === id);
    if (!t) return null;
    const stages =
      t.pattern === "graph"
        ? compileGraph(t, st.solos, st.providers, st.tools, st.staticTpls, task, st.history)
        : compileSupervisor(t, st.solos, st.providers, st.tools, st.staticTpls, task, st.history);
    return { label: staticLabel(t), stages };
  }
  return null;
}

/** The stage whose closing message is the run's answer: a sink of the DAG —
 *  nothing depends on it — and the last one of those in compile order.
 *
 *  Chat needs this because a reply is one stage's text, not the run's. The host
 *  can fall back to "the most recently written handoff manifest", which is right
 *  in a serial run, but naming the stage is the difference between knowing and
 *  inferring. A run with a genuine fan-out at the end has several sinks and the
 *  last one wins — the alternative is concatenating answers that were written
 *  without knowing about each other. */
export function sinkStageId(stages: RunStage[]): string {
  if (stages.length === 0) return "";
  const depended = new Set(stages.flatMap((s) => s.dependsOn));
  const sinks = stages.filter((s) => !depended.has(s.id));
  return (sinks.length > 0 ? sinks[sinks.length - 1] : stages[stages.length - 1]).id;
}

/** The runSpec object stored on a schedule (taskId/worktreePath are injected by
 * the host agent at fire time).
 *
 * `unattended` marks a run nobody is watching, which limits it to container
 * images approved for scheduled use. Pass it for anything Daily fires; leave it
 * off for a Delivery run, where a reviewer is in the drawer. The host agent also
 * forces the flag when it fires a schedule, so this is the UI's declaration of
 * intent rather than the enforcement point. */
export function buildRunSpec(stages: RunStage[], opts?: { unattended?: boolean }) {
  return {
    stages,
    // "shared" means every stage writes into ONE directory, so running two at
    // once would have them clobber each other. The orchestrator refuses the
    // combination outright — and it used to be built here regardless, so every
    // scheduled run was rejected with a 400 before a container ever started.
    // Concurrency needs "isolated", which needs a git worktree to branch per
    // stage; a Daily run writes into a plain output directory and has none, so
    // the honest value here is 1.
    worktreeMode: "shared" as const,
    maxParallel: 1,
    stopOnFailure: false,
    ...(opts?.unattended ? { unattended: true } : {}),
  };
}
