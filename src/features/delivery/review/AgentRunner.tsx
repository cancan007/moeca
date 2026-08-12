import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { DeliveryTask } from "@/store/useStore";
import { useStore } from "@/store/useStore";
import { sandbox, type RunStage, type RunStatus, type StageStatus } from "@/lib/sandbox";
import { hostagent } from "@/lib/hostagent";
import { delivery } from "@/lib/delivery";
import { runs } from "@/lib/runs";
import { compileRef, buildRunSpec, DYNAMIC_REF, type TemplateStores } from "@/lib/agentTemplates";
import { compileRouter } from "./compileTemplate";

/**
 * Resolve each stage's knowledge scope before a Delivery run starts.
 *
 * The task names a node of the Knowledge graph; each stage says how many
 * relations it may follow out of it. Both are resolved by the host agent, which
 * owns the graph — computing it here would mean two implementations of "what
 * does project X grant", and they would drift.
 *
 * A task with no scope resolves to nothing at all: the run stays unscoped and
 * searches everything, exactly as it did before scopes existed.
 */
async function withStageScopes(
  stages: RunStage[],
  repo: string,
  branch: string,
): Promise<{ stages: RunStage[]; runGroups: string[] | null }> {
  let meta;
  try {
    meta = await delivery.getTaskMeta(repo, branch);
  } catch {
    return { stages, runGroups: null };
  }
  if (!meta.scope) return { stages, runGroups: null };

  // One request per distinct depth: stages usually share one.
  const depths = [...new Set(stages.map((st) => st.knowledgeDepth ?? 0))];
  const resolved = new Map<number, string[]>();
  for (const d of depths) {
    try {
      const r = await delivery.resolveScope(meta.scope, d);
      resolved.set(d, r.groups ?? []);
    } catch {
      // A scope that cannot be resolved must not widen into "everything".
      resolved.set(d, []);
    }
  }
  return {
    stages: stages.map((st) => ({ ...st, groups: resolved.get(st.knowledgeDepth ?? 0) ?? [] })),
    runGroups: resolved.get(0) ?? [],
  };
}

const STATUS_COLOR: Record<StageStatus, string> = {
  pending: "var(--tx-faint)",
  running: "#4f9dff",
  done: "#67c9a4",
  failed: "var(--red)",
  skipped: "var(--tx-dim)",
  stopped: "#e0a83e",
};

const STATUS_KEY: Record<StageStatus, string> = {
  pending: "review.stage.pending", running: "review.stage.running", done: "review.stage.done",
  failed: "review.stage.failed", skipped: "review.stage.skipped", stopped: "review.stage.stopped",
};

/**
 * Runs a live Delivery task in a Docker sandbox, using whatever agent template
 * the task is assigned.
 *
 * There is one execution path. A single agent is a one-stage template, not a
 * special case: it compiles to a Stage DAG and goes through the orchestrator
 * like everything else, so run-scoped container names, per-stage progress and
 * the log archive apply uniformly. Dynamic is the same path run twice — a router
 * stage picks a template, then that template runs.
 *
 * The sandbox is strictly isolated: attached to the internal egress network, it
 * can reach only the gateway — not the host or the internet. The controller
 * derives the gateway URLs from the isolation mode, so this client never sets
 * them. Delivery tasks always work inside their own git worktree, so stages
 * share it rather than taking one each.
 */
export function AgentRunner({ task, templateRef }: { task: DeliveryTask; templateRef: string }) {
  const { t } = useTranslation();
  const staticTpls = useStore((s) => s.staticTpls);
  const solos = useStore((s) => s.solos);
  const providers = useStore((s) => s.providers);
  const tools = useStore((s) => s.tools);
  const dynamicPrompt = useStore((s) => s.dynamicPrompt);
  const addNotif = useStore((s) => s.addNotif);

  const [runId, setRunId] = useState<string | null>(null);
  const [status, setStatus] = useState<RunStatus | null>(null);
  const [stageLogs, setStageLogs] = useState<Record<string, string>>({});
  const [sel, setSel] = useState<string | null>(null);
  const [phase, setPhase] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const timer = useRef<number | null>(null);

  useEffect(() => () => { if (timer.current) window.clearInterval(timer.current); }, []);

  const stopPolling = () => { if (timer.current) window.clearInterval(timer.current); timer.current = null; };

  const tplStores: TemplateStores = { solos, staticTpls, providers, tools };
  const compiled = templateRef === DYNAMIC_REF ? null : compileRef(templateRef, tplStores, task.title);
  const label = templateRef === DYNAMIC_REF ? t("review.dynamicAuto") : compiled?.label ?? t("review.unassigned");

  const taskId = task.id.replace(/[^a-zA-Z0-9_-]/g, "-");

  const notifyRunDone = (st: RunStatus) => {
    const done = st.status === "done";
    const stages = st.stages ?? [];
    const failed = stages.filter((x) => x.status === "failed").length;
    const word = t(done ? "review.stage.done" : st.status === "stopped" ? "review.stage.stopped" : "review.stage.failed");
    addNotif({
      kind: "agent",
      tone: done ? "ok" : "error",
      title: t("review.runFinished", { title: task.title, word }),
      detail: `${t("review.stageCount", { count: stages.length })}${failed ? ` · ${t("review.failedCount", { count: failed })}` : ""} · ${task.project}`,
    });
  };

  const pollRun = (id: string) => {
    stopPolling();
    timer.current = window.setInterval(async () => {
      try {
        const [st, lg] = await Promise.all([sandbox.runStatus(id), sandbox.runLogs(id)]);
        setStatus(st);
        setStageLogs(lg.logs || {});
        if (st.status !== "running") {
          stopPolling();
          notifyRunDone(st);
        }
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    }, 2000);
  };

  // Delivery tasks own a git worktree, so stages share it and run serially.
  // Shared worktrees are only safe without concurrency — see the orchestrator.
  const launch = async (stages: RunStage[], label: string, ref: string): Promise<string> => {
    // Each stage's knowledge scope, resolved by the host: it owns the graph, so
    // "project X" means the same thing here as it does for a schedule. How far
    // a stage may follow relations comes from its agent template, which is why
    // this is per stage rather than once for the run.
    const scoped = await withStageScopes(stages, task.project, task.branch);
    const { runId: id } = await sandbox.runTemplate({
      taskId,
      worktreePath: task.worktreePath ?? "",
      isolation: "strict",
      ...buildRunSpec(scoped.stages),
      ...(scoped.runGroups ? { groups: scoped.runGroups } : {}),
      worktreeMode: "shared",
      maxParallel: 1,
      delegation: false,
    });
    runs
      .record({ name: task.id, repo: task.project, branch: task.branch, task: task.title, template: label, templateRef: ref, runId: id })
      .catch(() => {});
    return id;
  };

  const waitDone = async (id: string): Promise<RunStatus> => {
    for (;;) {
      const [st, lg] = await Promise.all([sandbox.runStatus(id), sandbox.runLogs(id)]);
      setStatus(st); setStageLogs(lg.logs || {});
      if (st.status !== "running") { notifyRunDone(st); return st; }
      await new Promise((r) => setTimeout(r, 2000));
    }
  };

  // Dynamic: a router stage writes .orchestra/route naming a template; compile
  // and run whichever it picked. Both phases are ordinary template runs.
  const runDynamic = async () => {
    const routerProvider = providers.find((p) => p.name === "anthropic") ?? providers.find((p) => p.kind === "model");
    const routerModel = routerProvider?.models[0] ?? "claude-opus-4-8";
    const catalog = staticTpls.map((t) => ({ id: t.id, name: t.name, desc: t.desc ?? "" }));
    const routerStages = compileRouter(dynamicPrompt, routerProvider, routerModel, catalog, task.title);
    if (routerStages.length === 0) throw new Error(t("review.noDynamicProvider"));

    setPhase(t("review.routerRunning"));
    const rid = await launch(routerStages, "Dynamic — router", DYNAMIC_REF);
    setRunId(rid);
    const rst = await waitDone(rid);
    if (rst.status !== "done") throw new Error(t("review.routerFailed"));

    let choice = "";
    try { choice = (await hostagent.file(task.project, task.branch, ".orchestra/route")).trim(); } catch { /* ignore */ }
    const chosen = staticTpls.find((t) => t.id === choice) ?? staticTpls.find((t) => choice.includes(t.id));
    if (!chosen) throw new Error(t("review.routerUnparsable", { choice: choice || t("review.noOutput") }));

    const next = compileRef(`static:${chosen.id}`, tplStores, task.title);
    if (!next || next.stages.length === 0) throw new Error(t("review.chosenTemplateEmpty"));
    setPhase(t("review.runningChosen", { name: next.label }));
    setStatus(null); setStageLogs({});
    const id2 = await launch(next.stages, next.label, `static:${chosen.id}`);
    setRunId(id2); pollRun(id2);
  };

  const run = async () => {
    setBusy("run"); setErr(null);
    setStatus(null); setStageLogs({}); setSel(null); setPhase(null);
    try {
      if (templateRef === DYNAMIC_REF) {
        await runDynamic();
      } else {
        if (!compiled || compiled.stages.length === 0) throw new Error(t("review.templateEmpty"));
        const id = await launch(compiled.stages, compiled.label, templateRef);
        setRunId(id); pollRun(id);
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const stop = async () => {
    if (!runId) return;
    setBusy("stop");
    try { await sandbox.runStop(runId); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(null); }
  };

  const clear = async () => {
    stopPolling();
    setBusy("clear");
    // Removing the run drops its containers; the archived stage logs survive and
    // stay readable from the run history.
    if (runId) { try { await sandbox.runRemove(runId); } catch { /* ignore */ } }
    setRunId(null); setStatus(null); setStageLogs({}); setSel(null); setErr(null); setPhase(null);
    setBusy(null);
  };

  const active = status?.status === "running";

  return (
    <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "11px 13px", display: "flex", flexDirection: "column", gap: 9 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="var(--tx3)" strokeWidth="1.5"><rect x="2" y="2.5" width="12" height="11" rx="1.5" /><path d="M5 6l2 2-2 2M9 10h2" /></svg>
        <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("review.sandboxRun")}</span>
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{label} · {t("review.sandboxNote")}</span>
        <div style={{ flex: 1 }} />
        {runId ? (
          <>
            <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: STATUS_COLOR[(status?.status ?? "pending") as StageStatus] }}>{status?.status ?? "…"}</span>
            {active && <div onClick={stop} style={btnStyle}>{busy === "stop" ? t("review.stopping") : t("review.stop")}</div>}
            <div onClick={clear} style={btnStyle}>{busy === "clear" ? t("review.clearing") : t("review.clear")}</div>
          </>
        ) : (
          <div onClick={() => !busy && run()} style={{ display: "flex", alignItems: "center", gap: 6, font: "600 10.5px 'IBM Plex Sans'", color: "var(--ac)", cursor: busy ? "not-allowed" : "pointer", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)" }}>
            <svg width="12" height="12" viewBox="0 0 14 14" fill="var(--ac)"><path d="M4 3l7 4-7 4z" /></svg>
            {busy === "run" ? t("review.starting") : t("review.run")}
          </div>
        )}
      </div>

      {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</div>}
      {phase && <div style={{ font: "500 9.5px 'IBM Plex Mono'", color: "#c79ae0" }}>{phase}</div>}

      {status && (
        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          {status.stages.map((s) => (
            <div key={s.id}>
              <div onClick={() => setSel(sel === s.id ? null : s.id)} style={{ display: "flex", alignItems: "center", gap: 8, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "7px 10px", cursor: "pointer" }}>
                <div style={{ width: 7, height: 7, borderRadius: "50%", background: STATUS_COLOR[s.status], flex: "none", ...(s.status === "running" ? { boxShadow: `0 0 6px ${STATUS_COLOR[s.status]}` } : {}) }} />
                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{s.name}</span>
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{s.role}</span>
                {s.dependsOn.length > 0 && <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>← {s.dependsOn.join(", ")}</span>}
                <div style={{ flex: 1 }} />
                <span style={{ font: "500 9px 'IBM Plex Mono'", color: STATUS_COLOR[s.status] }}>{t(STATUS_KEY[s.status])}{s.status === "failed" && s.exitCode ? `(${s.exitCode})` : ""}</span>
              </div>
              {/* A stage that failed before it had a container has no log to
                  expand, so its reason is shown up front rather than behind a
                  click that would only ever reveal "no log". */}
              {s.error && (
                <div style={{ margin: "4px 0 0", background: "var(--bg-deep)", border: "1px solid var(--bd3)", borderRadius: 6, padding: "7px 10px", font: "400 9.5px 'IBM Plex Mono'", color: "var(--red)", lineHeight: 1.6, wordBreak: "break-word" }}>
                  {s.error}
                </div>
              )}
              {sel === s.id && (
                <pre style={{ margin: "4px 0 0", maxHeight: 180, overflow: "auto", background: "var(--bg-deep)", border: "1px solid var(--bd3)", borderRadius: 6, padding: "9px 11px", fontFamily: "'IBM Plex Mono',monospace", fontSize: 10, lineHeight: 1.6, color: "var(--tx3)", whiteSpace: "pre-wrap" }}>
                  {stageLogs[s.id] || t("review.noStageLog")}
                </pre>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const btnStyle: React.CSSProperties = { font: "500 10px 'IBM Plex Sans'", color: "var(--tx3)", cursor: "pointer", padding: "4px 9px", border: "1px solid var(--bd2)", borderRadius: 6 };
