import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { useStore, type ReviewTab } from "@/store/useStore";
import { hostagent, type DiffFile, type PullRequest } from "@/lib/hostagent";
import { delivery as deliveryApi, type Milestone } from "@/lib/delivery";
import { templateOptions, normalizeRef, DYNAMIC_REF, type TemplateStores } from "@/lib/agentTemplates";
import { knowledge as knowledgeApi } from "@/lib/knowledge";
import type { KnowledgeScope } from "@/lib/schedules";
import { DiffPane } from "./review/DiffPane";
import { SourcePane } from "./review/SourcePane";
import { ArtifactsPane } from "./review/ArtifactsPane";
import { EvidencePane } from "./review/EvidencePane";
import { TaskDetailPane } from "./review/TaskDetailPane";
import { AgentRunner } from "./review/AgentRunner";

// TaskGoal — per-task goal + milestones (persisted by repo/branch). Checkboxes
// toggle milestone completion inline; the goal/milestones are edited in place.
function TaskGoal({ repo, branch }: { repo: string; branch: string }) {
  const { t } = useTranslation();
  const [goal, setGoal] = useState("");
  const [milestones, setMilestones] = useState<Milestone[]>([]);
  const [editing, setEditing] = useState(false);
  const [draftGoal, setDraftGoal] = useState("");
  const [draftMs, setDraftMs] = useState<string[]>([""]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    deliveryApi
      .getTaskMeta(repo, branch)
      .then((m) => {
        setGoal(m.goal ?? "");
        setMilestones(m.milestones ?? []);
      })
      .catch(() => {});
  }, [repo, branch]);

  const persist = async (g: string, ms: Milestone[]) => {
    try {
      await deliveryApi.setTaskMeta(repo, branch, g, ms);
      setErr(null);
      return true;
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      return false;
    }
  };

  const toggle = async (i: number) => {
    const next = milestones.map((m, j) => (j === i ? { ...m, done: !m.done } : m));
    setMilestones(next);
    persist(goal, next);
  };

  const startEdit = () => {
    setDraftGoal(goal);
    setDraftMs(milestones.length ? milestones.map((m) => m.title) : [""]);
    setEditing(true);
  };

  const save = async () => {
    const ms = draftMs.map((t) => t.trim()).filter(Boolean);
    if (draftGoal.trim() && ms.length === 0) {
      setErr(t("daily.goalNeedsMilestone"));
      return;
    }
    // keep existing done-flags where the title is unchanged
    const merged: Milestone[] = ms.map((t) => ({ title: t, done: milestones.find((m) => m.title === t)?.done ?? false }));
    if (await persist(draftGoal.trim(), merged)) {
      setGoal(draftGoal.trim());
      setMilestones(merged);
      setEditing(false);
    }
  };

  const done = milestones.filter((m) => m.done).length;

  return (
    <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "11px 13px", display: "flex", flexDirection: "column", gap: 8 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--ac)" }}>GOAL</span>
        {!editing && milestones.length > 0 && (
          <span style={{ marginLeft: "auto", font: "500 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{done}/{milestones.length}</span>
        )}
        {!editing && (
          <span onClick={startEdit} style={{ marginLeft: milestones.length > 0 ? 8 : "auto", font: "500 10px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer" }}>
            {goal ? t("common.edit") : `＋ ${t("review.setGoal")}`}
          </span>
        )}
      </div>
      {err && <div style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</div>}

      {!editing && goal && (
        <>
          <div style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{goal}</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
            {milestones.map((m, i) => (
              <div key={i} onClick={() => toggle(i)} style={{ display: "flex", alignItems: "center", gap: 7, cursor: "pointer" }}>
                <div style={{ width: 13, height: 13, borderRadius: 4, border: `1.5px solid ${m.done ? "#5fbf95" : "var(--bd3)"}`, background: m.done ? "#5fbf95" : "transparent", display: "flex", alignItems: "center", justifyContent: "center", flex: "none" }}>
                  {m.done && <svg width="9" height="9" viewBox="0 0 12 12" fill="none" stroke="#06121e" strokeWidth="2"><path d="M2.5 6.5L5 9l4.5-5" /></svg>}
                </div>
                <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: m.done ? "var(--tx-faint)" : "var(--tx2)", textDecoration: m.done ? "line-through" : "none" }}>{m.title}</span>
              </div>
            ))}
          </div>
        </>
      )}

      {editing && (
        <>
          <input value={draftGoal} onChange={(e) => setDraftGoal(e.target.value)} placeholder={t("review.goalPlaceholder")} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 10px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }} />
          {draftGoal.trim() && (
            <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
              {draftMs.map((m, i) => (
                <div key={i} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <input value={m} onChange={(e) => setDraftMs((xs) => xs.map((x, j) => (j === i ? e.target.value : x)))} placeholder={t("daily.milestoneN", { n: i + 1 })} style={{ flex: 1, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 6, padding: "6px 9px", font: "500 11px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }} />
                  {draftMs.length > 1 && <div onClick={() => setDraftMs((xs) => xs.filter((_, j) => j !== i))} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 13px 'IBM Plex Sans'", padding: "0 3px" }}>✕</div>}
                </div>
              ))}
              <div onClick={() => setDraftMs((xs) => [...xs, ""])} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer" }}>＋ {t("daily.addMilestone")}</div>
            </div>
          )}
          <div style={{ display: "flex", gap: 8 }}>
            <div onClick={save} style={{ font: "600 10.5px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "6px 13px", borderRadius: 7, cursor: "pointer" }}>{t("common.save")}</div>
            <div onClick={() => { setEditing(false); setErr(null); }} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx3)", padding: "6px 13px", cursor: "pointer" }}>{t("common.cancel")}</div>
          </div>
        </>
      )}
    </div>
  );
}

// Dot colour per template kind, read off the ref prefix.
function refColor(ref: string): string {
  if (ref === DYNAMIC_REF) return "var(--purple)";
  if (ref.startsWith("solo:")) return "var(--avatar-mut)";
  return "var(--cyan)";
}

function reviewTabStyle(active: boolean): React.CSSProperties {
  return active
    ? { font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", padding: "6px 12px", background: "var(--bg-tab)", borderRadius: 7, cursor: "pointer" }
    : { font: "500 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "6px 12px", cursor: "pointer" };
}

export function ReviewDrawer() {
  const { t } = useTranslation();
  const task = useStore((s) => s.tasks.find((t) => t.id === s.reviewId));
  const close = useStore((s) => s.closeReview);
  const tab = useStore((s) => s.reviewTab);
  const setTab = useStore((s) => s.setReviewTab);
  const moveTask = useStore((s) => s.moveTask);
  const refreshLive = useStore((s) => s.refreshLive);
  const navigate = useNavigate();

  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const providers = useStore((s) => s.providers);
  const tools = useStore((s) => s.tools);
  const tplStores: TemplateStores = { solos, staticTpls, providers, tools };
  const [tplId, setTplId] = useState("");
  const [tplOpen, setTplOpen] = useState(false);
  // The task's knowledge scope, and the graph to pick it from. Same three
  // states as a schedule: unset (retrieves nothing — no entitlement was
  // stated), Global (only what is everyone's), or a node of the graph.
  const [scope, setScope] = useState<KnowledgeScope | undefined>(undefined);
  const [tree, setTree] = useState<{ orgs: { id: string; name: string }[]; projects: { id: string; name: string; orgId: string }[] }>({ orgs: [], projects: [] });
  useEffect(() => {
    knowledgeApi.graph().then((g) => setTree({ orgs: g.orgs, projects: g.projects })).catch(() => {});
  }, []);
  const [diffFiles, setDiffFiles] = useState<DiffFile[] | undefined>(undefined);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [pullRequest, setPullRequest] = useState<PullRequest | null>(null);

  const live = !!task?.live;
  const repo = task?.project ?? "";
  const branch = task?.branch ?? "";

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
    if (task) window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [task, close]);

  useEffect(() => { setTplOpen(false); setActionErr(null); setPullRequest(null); }, [task?.id]);

  // Load the task's assigned agent template (persisted per repo/branch).
  useEffect(() => {
    if (!live) { setTplId(""); return; }
    let cancelled = false;
    deliveryApi.getTaskMeta(repo, branch)
      .then((m) => { if (!cancelled) { setTplId(m.template ?? ""); setScope(m.scope); } })
      .catch(() => { if (!cancelled) { setTplId(""); setScope(undefined); } });
    return () => { cancelled = true; };
  }, [live, repo, branch]);

  const assignScope = (next: KnowledgeScope | undefined) => {
    setScope(next);
    if (live) deliveryApi.setTaskScope(repo, branch, next ?? null).catch((e) => setActionErr(e instanceof Error ? e.message : String(e)));
  };

  const assignTemplate = (id: string) => {
    setTplId(id);
    setTplOpen(false);
    if (live) deliveryApi.setTaskTemplate(repo, branch, id).catch((e) => setActionErr(e instanceof Error ? e.message : String(e)));
  };

  // Load the real diff from the host agent for live tasks.
  useEffect(() => {
    if (!live) { setDiffFiles(undefined); return; }
    let cancelled = false;
    hostagent.diff(repo, branch).then((f) => { if (!cancelled) setDiffFiles(f); }).catch(() => { if (!cancelled) setDiffFiles([]); });
    return () => { cancelled = true; };
  }, [live, repo, branch]);

  if (!task) return null;
  const ciOk = task.ci === "passed";

  // Single-agent and multi-agent templates are the same kind of thing, so they
  // come from one list keyed by template ref — no separate "bare agent" entry.
  const tplOptions = templateOptions(tplStores, { includeDynamic: true });
  const activeRef = normalizeRef(tplId, tplStores);
  const activeTpl = tplOptions.find((o) => o.ref === activeRef) ?? tplOptions[0];

  const runCI = async () => {
    setBusy("ci"); setActionErr(null);
    try { await hostagent.ci(repo, branch); await refreshLive(); }
    catch (e) { setActionErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(null); }
  };
  const approve = async () => {
    if (!live) { moveTask(task.id, "done"); close(); return; }
    setBusy("merge"); setActionErr(null);
    try { await hostagent.merge(repo, branch); await refreshLive(); close(); }
    catch (e) { setActionErr(e instanceof Error ? e.message : String(e)); setBusy(null); }
  };
  // Opening a PR is deliberately not part of approve-and-merge: that lands the
  // branch locally and pushes nothing, so doing both would leave an empty PR.
  // The drawer stays open on success — the PR link is the result.
  const openPR = async () => {
    setBusy("pr"); setActionErr(null);
    try {
      const pr = await hostagent.createPR(repo, branch, task.title);
      setPullRequest(pr);
    } catch (e) {
      setActionErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };
  const reject = () => { if (!live) moveTask(task.id, "inbox"); close(); };

  // inbox = the agent has not produced changes yet, so the review surfaces
  // (diff / source / evidence / CI / approval) do not apply — task doc only.
  const isInbox = task.status === "inbox";
  const tabs: { id: ReviewTab; label: string }[] = isInbox
    ? [{ id: "task", label: t("review.tabs.task") }]
    : [
        { id: "task", label: t("review.tabs.task") },
        { id: "diff", label: t("review.tabs.diff") },
        { id: "source", label: t("review.tabs.source") },
        { id: "artifacts", label: t("review.tabs.artifacts") },
        { id: "evidence", label: t("review.tabs.evidence") },
      ];
  const activeTab: ReviewTab = tabs.some((t) => t.id === tab) ? tab : "task";

  return (
    <div style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", display: "flex", justifyContent: "flex-end", zIndex: 40 }} onClick={close}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: "62%", minWidth: 680, height: "100%", background: "var(--bg-panel)", borderLeft: "1px solid var(--bd)", display: "flex", flexDirection: "column", boxShadow: "-12px 0 40px rgba(0,0,0,.4)" }}>
        <div style={{ flex: 1, minHeight: 0, overflowY: "auto", display: "flex", flexDirection: "column" }}>
          {/* header */}
          <div style={{ padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", flexDirection: "column", gap: 11 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
              <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-thumb)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 5 }}>{task.id}</span>
              <div style={{ font: "600 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{task.title}</div>
              <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--ac)", background: "var(--tint-accent)", padding: "3px 7px", borderRadius: 5 }}>review</span>
              <div style={{ flex: 1 }} />
              <div onClick={close} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 18px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
            </div>

            <div style={{ display: "flex", alignItems: "center", gap: 8, font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
              <span>{task.branch}</span><span style={{ color: "var(--ac)" }}>→</span><span style={{ color: "#67c9a4" }}>{task.target}</span>
              <span style={{ color: "var(--bd-sep)" }}>·</span><span style={{ color: "var(--green)" }}>{task.add}</span><span style={{ color: "var(--red)" }}>{task.del}</span>
              <div style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: 6 }}>
                <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("review.assignee")}</span>
                <div style={{ width: 15, height: 15, borderRadius: "50%", background: task.agentGradient ? "linear-gradient(135deg,#4f9dff,#34d3e0)" : "var(--avatar-mut)" }} />
                <span style={{ color: "var(--tx-mut)" }}>{task.agent}</span>
              </div>
            </div>

            {/* worktree */}
            <div style={{ display: "flex", alignItems: "center", gap: 7, font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
              <svg width="12" height="12" viewBox="0 0 14 14" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M4 2v6a2 2 0 0 0 2 2h4M4 2a1.5 1.5 0 1 1 0 .01M10 10a1.5 1.5 0 1 1 0 .01M4 8V4" /></svg>
              <span>worktree {task.worktree}</span>
              <span className="oc-active-dot" style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--green)", marginLeft: 4 }} />
              <span style={{ color: "#67c9a4" }}>active</span>
            </div>

            {/* goal + milestones (live tasks, persisted by repo/branch) */}
            {task.live && <TaskGoal repo={task.project} branch={task.branch} />}

            {/* agent template assignment — what a sandbox run will execute */}
            <div style={{ position: "relative", display: "flex", alignItems: "center", gap: 9, background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "10px 13px" }}>
              <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><circle cx="4" cy="8" r="2" /><circle cx="12" cy="4" r="2" /><circle cx="12" cy="12" r="2" /><path d="M6 8h2a2 2 0 0 0 2-2M6 8h2a2 2 0 0 1 2 2" /></svg>
              <span style={{ font: "600 10.5px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t("review.agentAssignment")}</span>
              <div style={{ width: 8, height: 8, borderRadius: "50%", background: refColor(activeTpl?.ref ?? "") }} />
              <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{activeTpl?.label ?? t("review.noTemplate")}</span>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{activeTpl?.sub ?? ""}</span>
              <div style={{ flex: 1 }} />
              <div onClick={() => setTplOpen((v) => !v)} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "4px 10px", border: "1px solid var(--tint-active-bd)", borderRadius: 6, background: "var(--tint-active)" }}>{t("review.change")}</div>
              {tplOpen && (
                <div style={{ position: "absolute", top: 44, right: 0, width: 300, maxHeight: 320, overflowY: "auto", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 10, boxShadow: "0 14px 40px rgba(0,0,0,.4)", zIndex: 20 }}>
                  {tplOptions.map((o) => {
                    const sel = o.ref === activeTpl?.ref;
                    return (
                      <div key={o.ref} onClick={() => assignTemplate(o.ref)} style={{ display: "flex", alignItems: "center", gap: 9, padding: "9px 12px", cursor: "pointer", background: sel ? "var(--tint-active)" : "transparent", borderBottom: "1px solid var(--bd3)" }}>
                        <div style={{ width: 8, height: 8, borderRadius: "50%", background: refColor(o.ref), flex: "none" }} />
                        <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0, flex: 1 }}>
                          <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{o.label}</span>
                          <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{o.sub}</span>
                        </div>
                        {sel && <svg width="12" height="12" viewBox="0 0 14 14" fill="none" stroke="var(--ac)" strokeWidth="2.2"><path d="M2.5 7.5l3 3 6-7" /></svg>}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Knowledge scope: what this task's agents may retrieve. How far
                each of them may then follow relations is a property of the
                agent template, not of the task. */}
            <div style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "10px 13px" }}>
              <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M2 4.5C2 3 4.7 2 8 2s6 1 6 2.5V11c0 1.5-2.7 2.5-6 2.5S2 12.5 2 11z" /><path d="M2 4.5C2 6 4.7 7 8 7s6-1 6-2.5" /></svg>
              <span style={{ font: "600 10.5px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t("daily.knowledgeScope")}</span>
              <div style={{ flex: 1 }} />
              <select
                value={scope ? `${scope.kind}:${scope.id ?? ""}` : ""}
                onChange={(e) => {
                  const v = e.target.value;
                  if (!v) { assignScope(undefined); return; }
                  const [kind, id] = v.split(":");
                  assignScope({ kind: kind as KnowledgeScope["kind"], id: id || undefined });
                }}
                style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "6px 9px", font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx)", outline: "none", maxWidth: 260 }}
              >
                <option value="">{t("daily.scopeUnset")}</option>
                <option value="global:">{t("daily.scopeGlobal")}</option>
                {tree.orgs.map((o) => (
                  <optgroup key={o.id} label={o.name}>
                    <option value={`organization:${o.id}`}>{t("daily.scopeWholeOrg", { name: o.name })}</option>
                    {tree.projects.filter((p) => p.orgId === o.id).map((p) => (
                      <option key={p.id} value={`project:${p.id}`}>{p.name}</option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </div>

            {/* CI gate + approval (review stage only — hidden for inbox) */}
            {!isInbox && (<>
            <div style={{ background: "var(--bg-card)", border: `1px solid ${ciOk ? "var(--tint-green-bd)" : "var(--tint-red-bd)"}`, borderRadius: 9, padding: "11px 13px", display: "flex", flexDirection: "column", gap: 9 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("review.ciCheck")}</span>
                <span style={{ font: "500 9px 'IBM Plex Mono'", color: ciOk ? "var(--green)" : task.ci === "failed" ? "var(--red)" : "var(--tx-dim)", background: ciOk ? "var(--tint-green)" : task.ci === "failed" ? "var(--tint-red)" : "var(--bg-card2)", padding: "2px 6px", borderRadius: 4 }}>
                  {live ? t(task.ci === "passed" ? "common.passed" : task.ci === "failed" ? "common.failed" : task.ci === "running" ? "common.running" : "review.ciNotRun") : ciOk ? `5 / 5 ${t("common.passed")}` : `3 / 5 ${t("common.failed")}`}
                </span>
                <div style={{ flex: 1 }} />
                {live && task.ci !== "passed" && (
                  <div onClick={runCI} style={{ font: "600 10px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "4px 10px", border: "1px solid var(--tint-active-bd)", borderRadius: 6, background: "var(--tint-active)" }}>{busy === "ci" ? t("review.ciRunning") : t("review.runCI")}</div>
                )}
                <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: ciOk ? "#67c9a4" : "var(--red)" }}>{ciOk ? `✓ ${t("review.selfReviewOk")}` : `✕ ${t("review.selfReviewBlocked")}`}</span>
              </div>
              {!live && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                  {[["lint", true], ["typecheck", ciOk], ["unit (24)", true], ["build", ciOk], ["e2e", ciOk]].map(([name, ok]) => (
                    <span key={name as string} style={{ display: "flex", alignItems: "center", gap: 5, font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "3px 8px", borderRadius: 5 }}>
                      {ok ? <svg width="10" height="10" viewBox="0 0 14 14" fill="none" stroke="var(--green)" strokeWidth="2.2"><path d="M2.5 7.5l3 3 6-7" /></svg>
                        : <svg width="10" height="10" viewBox="0 0 14 14" fill="none" stroke="var(--red)" strokeWidth="2"><path d="M4 4l6 6M10 4l-6 6" /></svg>}
                      {name}
                    </span>
                  ))}
                </div>
              )}
            </div>

            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <div onClick={() => ciOk && approve()} style={{ font: "600 12px 'IBM Plex Sans'", color: ciOk ? "#0e1411" : "var(--tx-faint)", background: ciOk ? "var(--green)" : "var(--bg-card2)", border: ciOk ? "none" : "1px solid var(--bd2)", padding: "8px 16px", borderRadius: 7, cursor: ciOk ? "pointer" : "not-allowed" }}>{busy === "merge" ? t("review.merging") : t("review.approveMerge")}</div>
              {live && (
                <div onClick={() => !busy && openPR()} style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--ac)", background: "var(--tint-active)", border: "1px solid var(--tint-active-bd)", padding: "8px 15px", borderRadius: 7, cursor: busy ? "not-allowed" : "pointer" }}>{busy === "pr" ? t("review.prCreating") : t("review.createPR")}</div>
              )}
              <div onClick={reject} style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--txb)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "8px 15px", borderRadius: 7, cursor: "pointer" }}>{t("review.sendBack")}</div>
              {pullRequest && (
                <a href={pullRequest.url} target="_blank" rel="noreferrer" style={{ font: "500 10.5px 'IBM Plex Mono'", color: "#67c9a4", textDecoration: "none" }}>
                  {pullRequest.created ? "PR #" : `${t("review.existingPR")} #`}{pullRequest.number} → {pullRequest.base}
                </a>
              )}
              {actionErr && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{actionErr}</span>}
            </div>
            </>)}

            {live && <AgentRunner task={task} templateRef={activeRef} />}
          </div>

          {/* tabs */}
          <div style={{ display: "flex", alignItems: "center", gap: 7, padding: "11px 16px", borderBottom: "1px solid var(--bd)" }}>
            {tabs.map((t) => (
              <div key={t.id} onClick={() => setTab(t.id)} style={reviewTabStyle(activeTab === t.id)}>{t.label}</div>
            ))}
          </div>

          {activeTab === "task" && <TaskDetailPane repo={repo} branch={branch} live={live} />}
          {activeTab === "diff" && <DiffPane files={live ? diffFiles : undefined} />}
          {activeTab === "source" && <SourcePane task={task} onOpenWorkspace={() => navigate("/workspace")} />}
          {activeTab === "artifacts" && <ArtifactsPane task={task} />}
          {activeTab === "evidence" && <EvidencePane task={task} />}
        </div>

        {/* A2A footer */}
        <div style={{ borderTop: "1px solid var(--bd)", padding: "11px 16px", display: "flex", alignItems: "center", gap: 12, background: "var(--bg-deep)" }}>
          <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>A2A LOG</span>
          <div style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-mut)" }}><div style={{ width: 5, height: 5, borderRadius: "50%", background: "#4f9dff" }} />Planner → Builder</div>
          <div style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-mut)" }}><div style={{ width: 5, height: 5, borderRadius: "50%", background: "#34d3e0" }} />Builder → Reviewer</div>
          <div style={{ marginLeft: "auto", font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>42 events · 18.4k tok</div>
        </div>
      </div>
    </div>
  );
}
