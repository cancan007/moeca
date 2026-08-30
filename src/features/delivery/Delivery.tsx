import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { useStore, type DeliveryStatus } from "@/store/useStore";
import { DeliveryCard } from "./DeliveryCard";
import { ReviewDrawer } from "./ReviewDrawer";
import { delivery as deliveryApi, type Issue, type RepoInfo } from "@/lib/delivery";
import { hostagent } from "@/lib/hostagent";
import { runs as runsApi, type AgentRun } from "@/lib/runs";
import { RunArtifacts } from "@/features/runs/RunArtifacts";
import { RunOptimizer } from "@/features/runs/RunOptimizer";

const projects = [
  { name: "web-app", color: "var(--ac)", count: 4, active: true },
  { name: "api", color: "#67c9a4", count: 2, active: false },
  { name: "infra", color: "var(--amber)", count: 1, active: false },
  { name: "docs", color: "var(--purple)", count: 1, active: false },
];

function Column({ status, label, sub, dotColor }: { status: DeliveryStatus; label: string; sub: string; dotColor: string }) {
  const allTasks = useStore((s) => s.tasks);
  const tasks = allTasks.filter((t) => t.status === status);
  const moveTask = useStore((s) => s.moveTask);
  const [over, setOver] = useState(false);

  return (
    <div
      onDragOver={(e) => { e.preventDefault(); setOver(true); }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => { e.preventDefault(); setOver(false); const id = e.dataTransfer.getData("text/plain"); if (id) moveTask(id, status); }}
      style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, borderRadius: 10, background: over ? "var(--bg-card)" : "transparent", outline: over ? "1px dashed var(--tint-active-bd)" : "none", transition: "background .12s" }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7, padding: "6px 6px 11px" }}>
        <div style={{ width: 8, height: 8, borderRadius: "50%", background: dotColor }} />
        <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx2)" }}>{label}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{sub}</span>
        <span style={{ marginLeft: "auto", font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{tasks.length}</span>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 10, flex: 1, padding: "0 4px", overflowY: "auto" }}>
        {tasks.map((t) => <DeliveryCard key={t.id} task={t} />)}
      </div>
    </div>
  );
}

function NotConnected() {
  const { t } = useTranslation();
  const connecting = useStore((s) => s.connecting);
  const liveError = useStore((s) => s.liveError);
  const connect = useStore((s) => s.connectHostAgent);
  if (connecting) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", font: "500 12px 'IBM Plex Sans'", color: "var(--tx3)" }}>
        {t("daily.connecting")}
      </div>
    );
  }
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 10, padding: "0 40px" }}>
      <span style={{ font: "500 12px 'IBM Plex Sans'", color: "var(--tx3)", textAlign: "center" }}>{t("daily.hostUnreachable")}</span>
      {liveError && (
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)", textAlign: "center", wordBreak: "break-all" }}>{liveError}</span>
      )}
      <div onClick={() => connect()} style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "6px 14px", borderRadius: 7, border: "1px solid var(--tint-active-bd)", background: "var(--tint-active)" }}>
        {t("common.retry")}
      </div>
    </div>
  );
}

function LiveToggle() {
  const { t } = useTranslation();
  const source = useStore((s) => s.source);
  const connecting = useStore((s) => s.connecting);
  const liveError = useStore((s) => s.liveError);
  const connect = useStore((s) => s.connectHostAgent);
  const disconnect = useStore((s) => s.disconnectHostAgent);
  const live = source === "live";
  return (
    <div
      onClick={() => (live ? disconnect() : connect())}
      title={liveError ?? t(live ? "daily.hostConnected" : "daily.hostConnect")}
      style={{
        display: "flex", alignItems: "center", gap: 7, cursor: "pointer",
        padding: "5px 11px", borderRadius: 7,
        border: `1px solid ${live ? "var(--tint-green-bd)" : liveError ? "var(--tint-red-bd)" : "var(--bd2)"}`,
        background: live ? "var(--tint-green)" : liveError ? "var(--tint-red)" : "var(--bg-card2)",
      }}
    >
      <span className={live ? "oc-active-dot" : undefined} style={{ width: 7, height: 7, borderRadius: "50%", background: live ? "var(--green)" : liveError ? "var(--red)" : "var(--tx-dim)" }} />
      <span style={{ font: "500 11px 'IBM Plex Mono'", color: live ? "#67c9a4" : liveError ? "var(--red)" : "var(--tx3)" }}>
        {connecting ? t("daily.connecting") : live ? "host agent" : t("daily.hostRetry")}
      </span>
    </div>
  );
}

// GitHubIssues (live only): pull assigned GitHub issues and promote one into a
// worktree. Repo/base come from the matching configured repo when the mapping is
// clear, else the user picks (default value + manual switch).
// CreateTask (live only): create a Delivery task locally — a new worktree/branch
// on a registered repo, no GitHub issue involved. The new card appears in inbox.
// NewTaskModal — create a Delivery task locally, per the Orchestra design's
// The "new task" modal: repo + base branch, title (→ branch slug), task detail
// (agent instructions), and an agent-template pick (solo / team). The host agent
// creates the worktree; the title+detail are written to .orchestra/task.md so
// they become the agent's task, and the template choice is recorded there too.
function NewTaskModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const refreshLive = useStore((s) => s.refreshLive);
  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const [repos, setRepos] = useState<RepoInfo[]>([]);
  const [repo, setRepo] = useState("");
  const [base, setBase] = useState("");
  const [title, setTitle] = useState("");
  const [detail, setDetail] = useState("");
  const [mode, setMode] = useState<"solo" | "team">("solo");
  const [tplId, setTplId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    deliveryApi
      .repos()
      .then((r) => {
        setRepos(r);
        setRepo((c) => c || r[0]?.name || "");
        setBase((c) => c || r[0]?.target || "main");
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  const onRepo = (name: string) => {
    setRepo(name);
    setBase(repos.find((r) => r.name === name)?.target || "main");
  };

  // Branch is derived from the title (design shows an auto preview, no branch input).
  const slug = title.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  const branch = title.trim() ? (slug ? `task/${slug}` : `task/t-${Date.now().toString(36).slice(-5)}`) : "";

  const templates = mode === "solo"
    ? solos.map((a) => ({ id: a.id, name: a.name, desc: [a.role, a.model].filter(Boolean).join(" · "), badge: "solo", color: a.dot }))
    : staticTpls.map((tpl) => ({
        id: tpl.id,
        name: tpl.name,
        desc: tpl.desc,
        badge: `${tpl.pattern === "graph" ? tpl.nodes.length : tpl.workers.length + 1} agents · ${tpl.pattern === "graph" ? t("delivery.graph") : t("delivery.supervisor")}`,
        color: tpl.pattern === "graph" ? "#4f9dff" : "#b08ad9",
      }));
  const selId = templates.some((x) => x.id === tplId) ? tplId : templates[0]?.id ?? null;
  const selTpl = templates.find((x) => x.id === selId);
  const valid = title.trim().length > 0 && !!repo && !busy;

  const create = async () => {
    if (!valid) return;
    setBusy(true);
    setErr(null);
    try {
      await deliveryApi.createTask(repo, branch, base);
      const body =
        `# ${title.trim()}\n\n${detail.trim()}\n` +
        (selId ? `\n<!-- orchestra: template=${mode}:${selId} (${selTpl?.name ?? ""}) -->\n` : "");
      await hostagent.writeFile(repo, branch, ".orchestra/task.md", body);
      await refreshLive();
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  const label: React.CSSProperties = { font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)" };
  const selectStyle: React.CSSProperties = { appearance: "none", background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 11px", font: "500 12px 'IBM Plex Mono'", color: "var(--tx)", outline: "none", cursor: "pointer" };
  const chip = (on: boolean): React.CSSProperties => ({ font: "600 10.5px 'IBM Plex Sans'", color: on ? "var(--ac)" : "var(--tx-dim)", background: on ? "var(--tint-active)" : "transparent", border: `1px solid ${on ? "var(--tint-active-bd)" : "var(--bd2)"}`, padding: "4px 11px", borderRadius: 6, cursor: "pointer" });

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.58)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 60, padding: 30 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 560, maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, boxShadow: "0 24px 70px rgba(0,0,0,.45)", display: "flex", flexDirection: "column", overflow: "hidden" }}>
        {/* header */}
        <div style={{ padding: "17px 22px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "700 15.5px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.2px" }}>{t("delivery.newTask")}</span>
          <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("delivery.worktreeAuto")}</span>
          <div style={{ flex: 1 }} />
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 18px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>

        {/* body */}
        <div style={{ padding: "20px 22px", display: "flex", flexDirection: "column", gap: 17, overflowY: "auto" }}>
          {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 8, padding: "8px 11px" }}>{err}</div>}

          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 14 }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
              <span style={label}>repo</span>
              <select value={repo} onChange={(e) => onRepo(e.target.value)} style={selectStyle}>
                {repos.length === 0 && <option value="">{t("delivery.noRepo")}</option>}
                {repos.map((r) => <option key={r.name} value={r.name}>{r.name}</option>)}
              </select>
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
              <span style={label}>{t("delivery.baseBranch")}</span>
              <select value={base} onChange={(e) => setBase(e.target.value)} style={selectStyle}>
                {(() => { const t = repos.find((r) => r.name === repo)?.target; return t ? <option value={t}>{t}</option> : <option value={base || "main"}>{base || "main"}</option>; })()}
              </select>
            </div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
            <span style={label}>{t("delivery.title")}</span>
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t("delivery.titlePlaceholder")} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "10px 12px", font: "500 13px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }} />
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={label}>{t("delivery.detail")}</span>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("delivery.detailHint")}</span>
            </div>
            <textarea value={detail} onChange={(e) => setDetail(e.target.value)} spellCheck={false} placeholder={t("delivery.detailPlaceholder")} style={{ height: 112, resize: "vertical", background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "11px 12px", font: "400 12px 'IBM Plex Mono'", lineHeight: 1.7, color: "var(--tx2)", outline: "none" }} />
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 9, paddingTop: 15, borderTop: "1px solid var(--bd-soft)" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
              <span style={label}>{t("daily.agentTemplate")}</span>
              <div style={{ flex: 1 }} />
              <div onClick={() => { setMode("solo"); setTplId(null); }} style={chip(mode === "solo")}>{t("delivery.solo")}</div>
              <div onClick={() => { setMode("team"); setTplId(null); }} style={chip(mode === "team")}>{t("delivery.team")}</div>
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {templates.length === 0 && <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("delivery.noTemplates")}</span>}
              {templates.map((t) => {
                const on = t.id === selId;
                return (
                  <div key={t.id} onClick={() => setTplId(t.id)} style={{ display: "flex", alignItems: "center", gap: 10, background: on ? "var(--tint-active)" : "var(--bg-card2)", border: `1px solid ${on ? "var(--tint-active-bd)" : "var(--bd2)"}`, borderRadius: 8, padding: "9px 11px", cursor: "pointer" }}>
                    <div style={{ width: 8, height: 8, borderRadius: "50%", background: t.color, flex: "none" }} />
                    <div style={{ display: "flex", flexDirection: "column", gap: 3, minWidth: 0, flex: 1 }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                        <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t.name}</span>
                        <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", padding: "2px 6px", borderRadius: 5 }}>{t.badge}</span>
                      </div>
                      <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t.desc}</span>
                    </div>
                    <div style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--ac)", opacity: on ? 1 : 0, flex: "none" }}>✓</div>
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        {/* footer */}
        <div style={{ padding: "14px 22px", borderTop: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10, background: "var(--bg-card)" }}>
          <svg width="12" height="12" viewBox="0 0 14 14" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M4 2v6a2 2 0 0 0 2 2h4M4 2a1.5 1.5 0 1 1 0 .01M10 10a1.5 1.5 0 1 1 0 .01M4 8V4" /></svg>
          <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {branch ? `${repo} / ${branch} ← ${base}` : t("delivery.branchFromTitle")}
          </span>
          <div style={{ flex: 1 }} />
          <div onClick={onClose} style={{ font: "500 11.5px 'IBM Plex Sans'", color: "var(--tx3)", padding: "7px 14px", border: "1px solid var(--bd2)", borderRadius: 7, cursor: "pointer" }}>{t("common.cancel")}</div>
          <div onClick={() => valid && create()} style={{ font: "600 12px 'IBM Plex Sans'", color: valid ? "#06121e" : "var(--tx-dim)", background: valid ? "var(--ac)" : "var(--bg-card2)", border: valid ? "none" : "1px solid var(--bd2)", padding: "7px 14px", borderRadius: 7, cursor: valid ? "pointer" : "default" }}>
            {busy ? t("delivery.creating") : t("delivery.createWithWorktree")}
          </div>
        </div>
      </div>
    </div>
  );
}

function GitHubIssues() {
  const { t } = useTranslation();
  const live = useStore((s) => s.source === "live");
  const [issues, setIssues] = useState<Issue[]>([]);
  const [repos, setRepos] = useState<RepoInfo[]>([]);
  const [pullRepo, setPullRepo] = useState("");
  const [pulling, setPulling] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!live) {
      setIssues([]);
      return;
    }
    deliveryApi.issues().then(setIssues).catch(() => {});
    deliveryApi.repos().then(setRepos).catch(() => {});
  }, [live]);

  if (!live) return null;

  // Auto-map a pulled issue to its registered local repo by GitHub slug (origin
  // owner/repo). The binding is established when the repo is registered — no
  // per-issue repo selection. Falls back to a name match for older records.
  const repoFor = (issue: Issue) => {
    const bySlug = repos.find((r) => r.githubSlug && r.githubSlug === issue.repo);
    if (bySlug) return bySlug.name;
    const byName = repos.find((r) => issue.repo === r.name || issue.repo.endsWith("/" + r.name));
    return byName?.name ?? "";
  };

  const pull = async () => {
    setPulling(true);
    setErr(null);
    try {
      setIssues((await deliveryApi.pull(pullRepo || undefined)).issues);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setPulling(false);
    }
  };

  const promote = async (issue: Issue) => {
    const repo = repoFor(issue);
    if (!repo) {
      setErr(t("delivery.repoNotRegistered", { repo: issue.repo }));
      return;
    }
    setBusy(issue.id);
    setErr(null);
    try {
      const base = repos.find((r) => r.name === repo)?.target;
      const res = await deliveryApi.promote(issue.id, repo, base);
      window.alert(t("delivery.promoted", { repo: res.repo, branch: res.branch }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "0 6px 8px" }}>
        <span style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.6px" }}>GITHUB ISSUES</span>
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{issues.length}</span>
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "0 6px 9px" }}>
        <select
          value={pullRepo}
          onChange={(e) => setPullRepo(e.target.value)}
          title={t("delivery.pullRepoTip")}
          style={{ flex: 1, minWidth: 0, background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 6, padding: "4px 6px", font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)", outline: "none" }}
        >
          <option value="">{t("delivery.allAssigned")}</option>
          {repos.map((r) => (
            <option key={r.name} value={r.name}>{r.name}</option>
          ))}
        </select>
        <div
          onClick={() => !pulling && pull()}
          title={pullRepo ? t("delivery.pullRepoIssues", { repo: pullRepo }) : t("delivery.pullAssigned")}
          style={{ flex: "none", font: "600 9.5px 'IBM Plex Mono'", color: "var(--ac)", cursor: pulling ? "default" : "pointer", padding: "4px 9px", border: "1px solid var(--tint-active-bd)", borderRadius: 6, background: "var(--tint-active)", whiteSpace: "nowrap" }}
        >
          {pulling ? t("daily.pulling") : t("delivery.pull")}
        </div>
      </div>
      {err && <div style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--red)", padding: "0 6px 6px" }}>{err}</div>}
      <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
        {issues.length === 0 && (
          <div style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "0 6px" }}>{t("delivery.noIssues")}</div>
        )}
        {issues.map((issue) => (
          <div key={issue.id} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "8px 9px", display: "flex", flexDirection: "column", gap: 6 }}>
            <span
              onClick={() => issue.url && window.open(issue.url, "_blank")}
              title={issue.url}
              style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx2)", cursor: issue.url ? "pointer" : "default", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
            >
              {issue.title}
            </span>
            <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{issue.repo}</span>
            <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
              {repoFor(issue) ? (
                <span title={t("delivery.targetRepoTip")} style={{ flex: 1, minWidth: 0, font: "500 9px 'IBM Plex Mono'", color: "var(--tx-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>→ {repoFor(issue)}</span>
              ) : (
                <span title={t("delivery.repoUnregisteredTip")} style={{ flex: 1, minWidth: 0, font: "500 9px 'IBM Plex Mono'", color: "#d39a4e", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{t("delivery.repoUnregistered")}</span>
              )}
              <div
                onClick={() => busy === null && repoFor(issue) && promote(issue)}
                title={t("delivery.promoteTip")}
                style={{ font: "600 9.5px 'IBM Plex Mono'", color: "#06121e", background: "var(--ac)", padding: "3px 10px", borderRadius: 6, cursor: busy || !repoFor(issue) ? "default" : "pointer", opacity: (busy && busy !== issue.id) || !repoFor(issue) ? 0.5 : 1, flex: "none" }}
              >
                {busy === issue.id ? "…" : t("delivery.promote")}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

// RunHistory (live only): persisted manual agent runs. Click one to see its
// artifacts + A2A logs; template runs open the same prompt-optimization loop.
function RunHistory() {
  const { t } = useTranslation();
  const live = useStore((s) => s.source === "live");
  const [items, setItems] = useState<AgentRun[]>([]);
  const [artifact, setArtifact] = useState<AgentRun | null>(null);
  const [optimize, setOptimize] = useState<AgentRun | null>(null);

  const refresh = () => runsApi.list(30).then(setItems).catch(() => {});
  useEffect(() => {
    if (!live) {
      setItems([]);
      return;
    }
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, [live]);

  if (!live) return null;

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "0 6px 9px" }}>
        <span style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.6px" }}>{t("daily.runHistory")}</span>
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{items.length}</span>
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {items.length === 0 && <div style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "0 6px" }}>{t("delivery.noManualRuns")}</div>}
        {items.map((r) => (
          <div key={r.id} onClick={() => setArtifact(r)} title={t("daily.showArtifacts")} style={{ display: "flex", alignItems: "center", gap: 7, padding: "7px 9px", background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, cursor: "pointer" }}>
            <div style={{ width: 6, height: 6, borderRadius: "50%", background: r.template ? "var(--ac)" : "var(--tx-dim)", flex: "none" }} />
            <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r.name || r.task}</span>
            {r.template && <span style={{ marginLeft: "auto", font: "500 8.5px 'IBM Plex Mono'", color: "var(--ac)", flex: "none" }}>tpl</span>}
          </div>
        ))}
      </div>
      {artifact && (
        <RunArtifacts
          run={artifact}
          onClose={() => setArtifact(null)}
          onOptimize={artifact.template && artifact.templateRef ? () => { setOptimize(artifact); setArtifact(null); } : undefined}
        />
      )}
      {optimize && optimize.templateRef && (
        <RunOptimizer templateRef={optimize.templateRef} templateLabel={optimize.template} onClose={() => setOptimize(null)} onSynced={refresh} />
      )}
    </div>
  );
}

export function Delivery() {
  const { t } = useTranslation();
  const live = useStore((s) => s.source === "live");
  const [ntOpen, setNtOpen] = useState(false);
  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
      {/* sidebar */}
      <div style={{ width: 224, flex: "none", background: "var(--bg-panel)", borderRight: "1px solid var(--bd)", padding: "16px 12px", display: "flex", flexDirection: "column", gap: 18, overflowY: "auto" }}>
        <div style={{ display: "flex", alignItems: "center", background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "7px 10px", gap: 7 }}>
          <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="var(--tx-dim)" strokeWidth="1.6"><circle cx="6" cy="6" r="4.5" /><path d="M9.5 9.5L12 12" /></svg>
          <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t("delivery.searchTasks")}</span>
        </div>

        <div>
          <div style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.6px", padding: "0 6px 9px" }}>PROJECTS</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {projects.map((p) => (
              <div key={p.name} style={{ display: "flex", alignItems: "center", gap: 9, padding: "7px 9px", borderRadius: 7, background: p.active ? "var(--tint-active)" : "transparent", cursor: "pointer" }}>
                <div style={{ width: 8, height: 8, borderRadius: 2, background: p.color }} />
                <span style={{ font: "500 12px 'IBM Plex Sans'", color: p.active ? "var(--tx2)" : "var(--tx3)" }}>{p.name}</span>
                <span style={{ marginLeft: "auto", font: "500 10px 'IBM Plex Mono'", color: p.active ? "#5b9fe8" : "var(--tx-dim)" }}>{p.count}</span>
              </div>
            ))}
          </div>
        </div>

        <div>
          <div style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.6px", padding: "0 6px 9px" }}>STATUS</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 8, padding: "0 6px" }}>
            {[[t("delivery.inbox"), "var(--amber)", 3], [t("delivery.working"), "var(--ac)", 2], [t("delivery.done"), "var(--green)", 3]].map(([l, c, n]) => (
              <div key={l as string} style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <div style={{ width: 7, height: 7, borderRadius: "50%", background: c as string }} />
                <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx3)" }}>{l}</span>
                <span style={{ marginLeft: "auto", font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{n}</span>
              </div>
            ))}
          </div>
        </div>

        <GitHubIssues />

        <RunHistory />

        <div style={{ marginTop: "auto", background: "var(--bg-inset)", border: "1px solid var(--bd3)", borderRadius: 8, padding: 12 }}>
          <div style={{ display: "flex", justifyContent: "space-between", font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)", marginBottom: 7 }}>
            <span>token / day</span><span style={{ color: "var(--cyan)" }}>1.2M</span>
          </div>
          <div style={{ height: 5, background: "var(--bd3)", borderRadius: 3, overflow: "hidden", marginBottom: 9 }}>
            <div style={{ width: "48%", height: "100%", background: "linear-gradient(90deg,#34d3e0,#4f9dff)" }} />
          </div>
          <div style={{ font: "400 9.5px 'IBM Plex Mono'", color: "#5a8f6f" }}>{t("delivery.compressionSaving")}</div>
        </div>
      </div>

      {/* board */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, background: "var(--bg-app)" }}>
        <div style={{ height: 48, flex: "none", display: "flex", alignItems: "center", padding: "0 18px", gap: 10, borderBottom: "1px solid var(--bd)" }}>
          <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>web-app</span>
          <span style={{ font: "400 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>acme/web-app · 4 tasks</span>
          <div style={{ flex: 1 }} />
          {live && (
            <div onClick={() => setNtOpen(true)} title={t("delivery.newTaskTip")} style={{ display: "flex", alignItems: "center", gap: 6, font: "600 11px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: "pointer" }}>
              <span style={{ font: "500 13px 'IBM Plex Sans'", lineHeight: 1 }}>+</span>{t("delivery.newTaskShort")}
            </div>
          )}
          <LiveToggle />
          <div style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)", padding: "5px 11px", border: "1px solid var(--bd2)", borderRadius: 7 }}>Board</div>
          <div style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "5px 11px" }}>List</div>
        </div>
        {/* Three empty columns and a failed connection look identical. Only one
            of them is something the user can act on, so say which this is. */}
        {!live ? (
          <NotConnected />
        ) : (
          <div style={{ flex: 1, display: "flex", gap: 14, padding: "16px 18px", overflow: "hidden" }}>
            <Column status="inbox" label={t("delivery.inbox")} sub="inbox" dotColor="var(--amber)" />
            <Column status="working" label={t("delivery.working")} sub="working" dotColor="var(--ac)" />
            <Column status="done" label={t("delivery.done")} sub="done" dotColor="var(--green)" />
          </div>
        )}
      </div>

      <ReviewDrawer />
      {ntOpen && <NewTaskModal onClose={() => setNtOpen(false)} />}
    </div>
  );
}
